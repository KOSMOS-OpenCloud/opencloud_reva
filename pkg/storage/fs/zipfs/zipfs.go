// Package zipfs provides read-only browsing of ZIP archive contents.
//
// It reads the ZIP Central Directory (last ~64KB) for listings and
// streams individual files via offset-based access — no full extraction.
// Used by decomposedfs to treat archive files as virtual directories.
package zipfs

import (
	"archive/zip"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	typespb "github.com/cs3org/go-cs3apis/cs3/types/v1beta1"

	"github.com/opencloud-eu/reva/v2/pkg/errtypes"
)

// PathSplit finds potential .zip/ boundaries in a path.
// Returns (zipPath, innerPath, found).
// This only does string analysis — the caller MUST verify that zipPath
// is actually a file (not a directory) before entering ZIP mode.
//
// Example: "/docs/archive.zip/readme.md" → ("/docs/archive.zip", "readme.md", true)
// Example: "/docs/archive.zip/" → ("/docs/archive.zip", "", true)
// Example: "/docs/archive.zip" → ("", "", false) — no slash after .zip = normal file
func PathSplit(p string) (zipPath, innerPath string, found bool) {
	lower := strings.ToLower(p)

	// Search for all .zip/ occurrences — there could be a directory named x.zip
	// containing a file named y.zip/... so we try each candidate
	search := lower
	offset := 0
	for {
		idx := strings.Index(search, ".zip/")
		if idx < 0 {
			break
		}
		candidate := p[:offset+idx+4] // include ".zip"
		rest := p[offset+idx+5:]      // after ".zip/"
		return candidate, strings.TrimPrefix(rest, "/"), true
	}

	// Trailing slash on .zip path = browse root
	if strings.HasSuffix(p, "/") {
		clean := strings.TrimSuffix(p, "/")
		if strings.HasSuffix(strings.ToLower(clean), ".zip") {
			return clean, "", true
		}
	}

	return "", "", false
}

// IsArchiveName checks if a filename has a known archive extension.
func IsArchiveName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".7z")
}

// IsFile checks if the given path is a regular file (not a directory).
// Use this after PathSplit to confirm the .zip path is actually a file
// before entering ZIP mode. A directory named "reports.zip" must not
// trigger archive browsing.
func IsFile(filePath string) bool {
	info, err := os.Stat(filePath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// CachedArchive holds a parsed ZIP Central Directory with an LRU timeout.
type CachedArchive struct {
	Reader   *zip.Reader
	file     *os.File // keep open for seeking
	Size     int64
	Modified time.Time
	LastUsed time.Time
}

// Cache is a thread-safe LRU cache for opened ZIP archives.
type Cache struct {
	entries map[string]*CachedArchive // key: absolute file path
	mu      sync.RWMutex
	maxAge  time.Duration
}

// NewCache creates a new ZIP archive cache.
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
// filePath must be the absolute path to the ZIP file on disk.
// Caller should verify IsFile(filePath) before calling this.
func (c *Cache) Get(filePath string) (*CachedArchive, error) {
	c.mu.RLock()
	if a, ok := c.entries[filePath]; ok {
		// Check if file was modified since caching
		stat, err := os.Stat(filePath)
		if err == nil && stat.ModTime().Equal(a.Modified) {
			a.LastUsed = time.Now()
			c.mu.RUnlock()
			return a, nil
		}
		// Modified or error — invalidate
		c.mu.RUnlock()
		c.Evict(filePath)
	} else {
		c.mu.RUnlock()
	}

	// Open and parse
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open zip %s: %w", filePath, err)
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat zip %s: %w", filePath, err)
	}
	zr, err := zip.NewReader(f, stat.Size())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("parse zip %s: %w", filePath, err)
	}

	a := &CachedArchive{
		Reader:   zr,
		file:     f,
		Size:     stat.Size(),
		Modified: stat.ModTime(),
		LastUsed: time.Now(),
	}

	c.mu.Lock()
	// Evict old entries while we're here
	for k, v := range c.entries {
		if time.Since(v.LastUsed) > c.maxAge {
			v.file.Close()
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
		a.file.Close()
		delete(c.entries, filePath)
	}
}

// Close closes all cached archives.
func (c *Cache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.entries {
		a.file.Close()
	}
	c.entries = make(map[string]*CachedArchive)
}

// globalCache is the shared cache instance.
var globalCache = NewCache(0)

// GlobalCache returns the shared ZIP archive cache.
func GlobalCache() *Cache {
	return globalCache
}

// ListFolder returns the contents of a directory inside the ZIP.
func ListFolder(a *CachedArchive, innerPath string, spaceID string) ([]*provider.ResourceInfo, error) {
	prefix := ""
	if innerPath != "" {
		prefix = strings.TrimSuffix(innerPath, "/") + "/"
	}

	seen := make(map[string]bool)
	var result []*provider.ResourceInfo

	for _, f := range a.Reader.File {
		name := f.Name
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel == "" {
			continue
		}

		parts := strings.SplitN(rel, "/", 2)
		childName := parts[0]
		if childName == "" || seen[childName] {
			continue
		}
		seen[childName] = true

		if len(parts) > 1 || strings.HasSuffix(name, "/") {
			// Path must be relative (just the child name) — the caller joins with the request path
			result = append(result, makeDirInfo(spaceID, childName, f.Modified))
		} else {
			result = append(result, makeFileInfoRelative(spaceID, childName, f))
		}
	}

	return result, nil
}

// Stat returns metadata for a path inside the ZIP.
func Stat(a *CachedArchive, innerPath string, spaceID string) (*provider.ResourceInfo, error) {
	if innerPath == "" {
		return makeRootInfo(a, spaceID), nil
	}

	clean := strings.TrimSuffix(innerPath, "/")

	// Try as file first
	for _, f := range a.Reader.File {
		n := strings.TrimSuffix(f.Name, "/")
		if n == clean && !strings.HasSuffix(f.Name, "/") {
			return makeFileInfo(spaceID, f), nil
		}
	}

	// Try as directory (implicit)
	dirPrefix := clean + "/"
	for _, f := range a.Reader.File {
		if strings.HasPrefix(f.Name, dirPrefix) {
			return makeDirInfo(spaceID, clean, f.Modified), nil
		}
	}

	return nil, errtypes.NotFound(innerPath)
}

// Download opens a file inside the ZIP for reading.
func Download(a *CachedArchive, innerPath string, spaceID string) (*provider.ResourceInfo, io.ReadCloser, error) {
	clean := strings.TrimSuffix(innerPath, "/")

	for _, f := range a.Reader.File {
		n := strings.TrimSuffix(f.Name, "/")
		if n == clean && !strings.HasSuffix(f.Name, "/") {
			rc, err := f.Open()
			if err != nil {
				return nil, nil, fmt.Errorf("open zip entry %s: %w", innerPath, err)
			}
			return makeFileInfo(spaceID, f), rc, nil
		}
	}

	return nil, nil, errtypes.NotFound(innerPath)
}

// --- helpers ---

func makeRootInfo(a *CachedArchive, spaceID string) *provider.ResourceInfo {
	return &provider.ResourceInfo{
		Type: provider.ResourceType_RESOURCE_TYPE_CONTAINER,
		Id:   makeID(spaceID, "/"),
		Path: "/",
		Size: uint64(a.Size),
		Mtime: &typespb.Timestamp{
			Seconds: uint64(a.Modified.Unix()),
		},
	}
}

func makeDirInfo(spaceID, childName string, modified time.Time) *provider.ResourceInfo {
	return &provider.ResourceInfo{
		Type: provider.ResourceType_RESOURCE_TYPE_CONTAINER,
		Id:   makeID(spaceID, childName),
		Path: childName,
		Name: path.Base(childName),
		Mtime: &typespb.Timestamp{
			Seconds: uint64(modified.Unix()),
		},
	}
}

func makeFileInfo(spaceID string, f *zip.File) *provider.ResourceInfo {
	return &provider.ResourceInfo{
		Type:     provider.ResourceType_RESOURCE_TYPE_FILE,
		Id:       makeID(spaceID, f.Name),
		Path:     "/" + f.Name,
		Name:     path.Base(f.Name),
		Size:     f.UncompressedSize64,
		Mtime:    &typespb.Timestamp{Seconds: uint64(f.Modified.Unix())},
		Checksum: &provider.ResourceChecksum{Type: provider.ResourceChecksumType_RESOURCE_CHECKSUM_TYPE_UNSET, Sum: fmt.Sprintf("%08x", f.CRC32)},
	}
}

func makeFileInfoRelative(spaceID string, childName string, f *zip.File) *provider.ResourceInfo {
	return &provider.ResourceInfo{
		Type:     provider.ResourceType_RESOURCE_TYPE_FILE,
		Id:       makeID(spaceID, f.Name),
		Path:     childName,
		Name:     childName,
		Size:     f.UncompressedSize64,
		Mtime:    &typespb.Timestamp{Seconds: uint64(f.Modified.Unix())},
		Checksum: &provider.ResourceChecksum{Type: provider.ResourceChecksumType_RESOURCE_CHECKSUM_TYPE_UNSET, Sum: fmt.Sprintf("%08x", f.CRC32)},
	}
}

func makeID(spaceID, p string) *provider.ResourceId {
	h := md5.Sum([]byte(p))
	return &provider.ResourceId{
		StorageId: spaceID,
		SpaceId:   spaceID,
		OpaqueId:  fmt.Sprintf("zip-%x", h[:8]),
	}
}
