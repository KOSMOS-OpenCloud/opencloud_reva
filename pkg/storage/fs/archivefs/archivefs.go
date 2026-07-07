// Package archivefs provides read-only browsing of archive/image contents.
//
// It supports any format that can be opened as an fs.FS:
//   - ZIP (via archive/zip)
//   - ISO9660, FAT, ext4, squashfs (via go-diskfs)
//
// Used by decomposedfs to treat archive files as virtual directories.
package archivefs

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	typespb "github.com/cs3org/go-cs3apis/cs3/types/v1beta1"

	"github.com/opencloud-eu/reva/v2/pkg/errtypes"
)

// --- Archive openers ---

// Opener opens an archive file and returns a standard fs.FS for browsing.
type Opener interface {
	Open(diskPath string) (fs.FS, io.Closer, error)
	CanHandle(name string) bool
	// Formats returns the archive formats this opener supports.
	Formats() []ArchiveFormat
}

// ArchiveFormat describes a browsable archive format.
type ArchiveFormat struct {
	Extension string   `json:"extension"` // e.g. ".zip"
	MimeTypes []string `json:"mimeTypes"` // e.g. ["application/zip"]
}

// registry of openers, checked in order
var openers []Opener

// RegisterOpener adds an archive opener. First registered = highest priority.
func RegisterOpener(o Opener) {
	openers = append(openers, o)
}

func findOpener(name string) Opener {
	for _, o := range openers {
		if o.CanHandle(name) {
			return o
		}
	}
	return nil
}

// IsArchiveName checks if a filename has a known archive extension.
func IsArchiveName(name string) bool {
	return findOpener(name) != nil
}

// SupportedFormats returns all browsable archive formats from registered openers.
func SupportedFormats() []ArchiveFormat {
	var formats []ArchiveFormat
	seen := make(map[string]bool)
	for _, o := range openers {
		for _, f := range o.Formats() {
			if !seen[f.Extension] {
				seen[f.Extension] = true
				formats = append(formats, f)
			}
		}
	}
	return formats
}

// --- Cache ---

// CachedArchive holds an opened archive filesystem with LRU timeout.
type CachedArchive struct {
	FS       fs.FS
	closer   io.Closer
	Size     int64
	Modified time.Time
	LastUsed time.Time
}

// Cache is a thread-safe LRU cache for opened archive filesystems.
type Cache struct {
	entries map[string]*CachedArchive
	mu      sync.RWMutex
	maxAge  time.Duration
}

// NewCache creates a new archive cache.
func NewCache(maxAge time.Duration) *Cache {
	if maxAge == 0 {
		maxAge = 5 * time.Minute
	}
	return &Cache{
		entries: make(map[string]*CachedArchive),
		maxAge:  maxAge,
	}
}

// Get returns a cached archive or opens a new one.
func (c *Cache) Get(filePath string) (*CachedArchive, error) {
	c.mu.RLock()
	if a, ok := c.entries[filePath]; ok {
		stat, err := os.Stat(filePath)
		if err == nil && stat.ModTime().Equal(a.Modified) {
			a.LastUsed = time.Now()
			c.mu.RUnlock()
			return a, nil
		}
		c.mu.RUnlock()
		c.Evict(filePath)
	} else {
		c.mu.RUnlock()
	}

	// Find opener for this file type
	opener := findOpener(filePath)
	if opener == nil {
		return nil, fmt.Errorf("archivefs: no opener for %s", filePath)
	}

	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("archivefs: stat %s: %w", filePath, err)
	}

	fsys, closer, err := opener.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("archivefs: open %s: %w", filePath, err)
	}

	a := &CachedArchive{
		FS:       fsys,
		closer:   closer,
		Size:     stat.Size(),
		Modified: stat.ModTime(),
		LastUsed: time.Now(),
	}

	c.mu.Lock()
	for k, v := range c.entries {
		if time.Since(v.LastUsed) > c.maxAge {
			if v.closer != nil {
				v.closer.Close()
			}
			delete(c.entries, k)
		}
	}
	c.entries[filePath] = a
	c.mu.Unlock()

	return a, nil
}

// Evict removes an archive from the cache.
func (c *Cache) Evict(filePath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a, ok := c.entries[filePath]; ok {
		if a.closer != nil {
			a.closer.Close()
		}
		delete(c.entries, filePath)
	}
}

// Close closes all cached archives.
func (c *Cache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.entries {
		if a.closer != nil {
			a.closer.Close()
		}
	}
	c.entries = make(map[string]*CachedArchive)
}

var globalCache = NewCache(0)

// GlobalCache returns the shared archive cache.
func GlobalCache() *Cache {
	return globalCache
}

// --- fs.FS-based ListFolder / Stat / Download ---

// ListFolder returns the contents of a directory inside the archive.
func ListFolder(a *CachedArchive, innerPath string, spaceID, archiveNodeID string) ([]*provider.ResourceInfo, error) {
	dir := innerPath
	if dir == "" {
		dir = "."
	}

	parentID := makeID(spaceID, archiveNodeID, innerPath)

	entries, err := fs.ReadDir(a.FS, dir)
	if err != nil {
		return nil, fmt.Errorf("archivefs: readdir %s: %w", dir, err)
	}

	var result []*provider.ResourceInfo
	for _, e := range entries {
		name := e.Name()
		fullPath := name
		if innerPath != "" {
			fullPath = innerPath + "/" + name
		}

		info, infoErr := e.Info()
		modified := a.Modified
		var size uint64
		if infoErr == nil {
			modified = info.ModTime()
			size = uint64(info.Size())
		}

		if e.IsDir() {
			result = append(result, &provider.ResourceInfo{
				Type:     provider.ResourceType_RESOURCE_TYPE_CONTAINER,
				Id:       makeID(spaceID, archiveNodeID, fullPath),
				ParentId: parentID,
				Path:     name,
				Name:     name,
				Etag:     makeEtag(fullPath, modified),
				Mtime:    &typespb.Timestamp{Seconds: uint64(modified.Unix())},
			})
		} else {
			result = append(result, &provider.ResourceInfo{
				Type:     provider.ResourceType_RESOURCE_TYPE_FILE,
				Id:       makeID(spaceID, archiveNodeID, fullPath),
				ParentId: parentID,
				Path:     name,
				Name:     name,
				Size:     size,
				MimeType: detectMimeType(name),
				Etag:     makeEtag(fullPath, modified),
				Mtime:    &typespb.Timestamp{Seconds: uint64(modified.Unix())},
			})
		}
	}

	return result, nil
}

// Stat returns metadata for a path inside the archive.
func Stat(a *CachedArchive, innerPath string, spaceID, archiveNodeID string) (*provider.ResourceInfo, error) {
	if innerPath == "" {
		return &provider.ResourceInfo{
			Type:  provider.ResourceType_RESOURCE_TYPE_CONTAINER,
			Id:    makeID(spaceID, archiveNodeID, ""),
			Path:  "/",
			Size:  uint64(a.Size),
			Mtime: &typespb.Timestamp{Seconds: uint64(a.Modified.Unix())},
		}, nil
	}

	info, err := fs.Stat(a.FS, innerPath)
	if err != nil {
		return nil, errtypes.NotFound(innerPath)
	}

	parentInnerPath := path.Dir(innerPath)
	if parentInnerPath == "." {
		parentInnerPath = ""
	}
	parentID := makeID(spaceID, archiveNodeID, parentInnerPath)

	if info.IsDir() {
		return &provider.ResourceInfo{
			Type:     provider.ResourceType_RESOURCE_TYPE_CONTAINER,
			Id:       makeID(spaceID, archiveNodeID, innerPath),
			ParentId: parentID,
			Path:     path.Base(innerPath),
			Name:     path.Base(innerPath),
			Etag:     makeEtag(innerPath, info.ModTime()),
			Mtime:    &typespb.Timestamp{Seconds: uint64(info.ModTime().Unix())},
		}, nil
	}

	return &provider.ResourceInfo{
		Type:     provider.ResourceType_RESOURCE_TYPE_FILE,
		Id:       makeID(spaceID, archiveNodeID, innerPath),
		ParentId: parentID,
		Path:     path.Base(innerPath),
		Name:     path.Base(innerPath),
		Size:     uint64(info.Size()),
		MimeType: detectMimeType(innerPath),
		Etag:     makeEtag(innerPath, info.ModTime()),
		Mtime:    &typespb.Timestamp{Seconds: uint64(info.ModTime().Unix())},
	}, nil
}

// Download opens a file inside the archive for reading.
func Download(a *CachedArchive, innerPath string, spaceID, archiveNodeID string) (*provider.ResourceInfo, io.ReadCloser, error) {
	f, err := a.FS.Open(innerPath)
	if err != nil {
		return nil, nil, errtypes.NotFound(innerPath)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("archivefs: stat %s: %w", innerPath, err)
	}

	if info.IsDir() {
		f.Close()
		return nil, nil, fmt.Errorf("archivefs: %s is a directory", innerPath)
	}

	ri := &provider.ResourceInfo{
		Type:     provider.ResourceType_RESOURCE_TYPE_FILE,
		Id:       makeID(spaceID, archiveNodeID, innerPath),
		Path:     path.Base(innerPath),
		Name:     path.Base(innerPath),
		Size:     uint64(info.Size()),
		MimeType: detectMimeType(innerPath),
		Etag:     makeEtag(innerPath, info.ModTime()),
		Mtime:    &typespb.Timestamp{Seconds: uint64(info.ModTime().Unix())},
	}

	// fs.File implements io.ReadCloser
	return ri, f, nil
}

// --- Helpers ---

func detectMimeType(name string) string {
	ext := path.Ext(name)
	if ext == "" {
		return "application/octet-stream"
	}
	mt := mime.TypeByExtension(ext)
	if mt == "" {
		return "application/octet-stream"
	}
	return mt
}

func makeEtag(name string, modified time.Time) string {
	h := md5.Sum([]byte(fmt.Sprintf("%s:%d", name, modified.UnixNano())))
	return fmt.Sprintf(`"%x"`, h[:8])
}

func makeID(spaceID, archiveNodeID, innerPath string) *provider.ResourceId {
	opaque := archiveNodeID + "!arc"
	if innerPath != "" {
		opaque += "." + base64.RawURLEncoding.EncodeToString([]byte(innerPath))
	}
	return &provider.ResourceId{
		SpaceId:  spaceID,
		OpaqueId: opaque,
	}
}

// ParseArchiveID checks if an OpaqueId is an archive reference and returns
// the archive node ID and inner path.
func ParseArchiveID(opaqueID string) (archiveNodeID, innerPath string, ok bool) {
	idx := strings.Index(opaqueID, "!arc")
	if idx < 0 {
		return "", "", false
	}
	archiveNodeID = opaqueID[:idx]
	rest := opaqueID[idx+4:]
	if rest == "" {
		return archiveNodeID, "", true
	}
	if rest[0] == '.' {
		decoded, err := base64.RawURLEncoding.DecodeString(rest[1:])
		if err != nil {
			return "", "", false
		}
		return archiveNodeID, string(decoded), true
	}
	return "", "", false
}
