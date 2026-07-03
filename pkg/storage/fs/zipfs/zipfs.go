// Package zipfs implements a read-only storage driver that presents
// ZIP archive contents as a virtual directory tree.
//
// It reads the ZIP Central Directory (last ~64KB) for listings and
// streams individual files via offset-based access — no full extraction.
package zipfs

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	typespb "github.com/cs3org/go-cs3apis/cs3/types/v1beta1"
	"github.com/rs/zerolog"

	"github.com/opencloud-eu/reva/v2/pkg/errtypes"
	"github.com/opencloud-eu/reva/v2/pkg/events"
	"github.com/opencloud-eu/reva/v2/pkg/storage"
	"github.com/opencloud-eu/reva/v2/pkg/storage/fs/registry"
)

func init() {
	registry.Register("zipfs", New)
}

// archive represents an opened ZIP file with its cached index.
type archive struct {
	zipPath  string        // path to the ZIP file on disk
	reader   *zip.Reader   // parsed central directory
	file     *os.File      // open file handle
	size     int64         // file size
	modified time.Time     // ZIP file mtime
	spaceID  string        // virtual space ID
	mu       sync.RWMutex
}

type zipfs struct {
	archives map[string]*archive // spaceID → archive
	mu       sync.RWMutex
	log      zerolog.Logger
}

// New creates a new zipfs storage driver.
func New(_ map[string]interface{}, _ events.Stream, _ zerolog.Logger) (storage.FS, error) {
	return &zipfs{
		archives: make(map[string]*archive),
		log:      zerolog.Nop(),
	}, nil
}

// OpenArchive opens a ZIP file and indexes its contents.
// Called externally when a user wants to browse a ZIP.
func (fs *zipfs) OpenArchive(zipPath string) (string, error) {
	f, err := os.Open(zipPath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return "", fmt.Errorf("stat zip: %w", err)
	}

	zr, err := zip.NewReader(f, stat.Size())
	if err != nil {
		f.Close()
		return "", fmt.Errorf("parse zip: %w", err)
	}

	// Generate a deterministic space ID from the file path
	h := md5.Sum([]byte(zipPath))
	spaceID := fmt.Sprintf("zipfs-%x", h[:8])

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Close existing archive for this path if any
	if old, ok := fs.archives[spaceID]; ok {
		old.file.Close()
	}

	fs.archives[spaceID] = &archive{
		zipPath:  zipPath,
		reader:   zr,
		file:     f,
		size:     stat.Size(),
		modified: stat.ModTime(),
		spaceID:  spaceID,
	}

	return spaceID, nil
}

// CloseArchive closes an opened archive.
func (fs *zipfs) CloseArchive(spaceID string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if a, ok := fs.archives[spaceID]; ok {
		a.file.Close()
		delete(fs.archives, spaceID)
	}
}

func (fs *zipfs) getArchive(spaceID string) (*archive, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	a, ok := fs.archives[spaceID]
	if !ok {
		return nil, errtypes.NotFound(spaceID)
	}
	return a, nil
}

// --- storage.FS interface (read-only subset) ---

func (fs *zipfs) Shutdown(_ context.Context) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, a := range fs.archives {
		a.file.Close()
	}
	fs.archives = make(map[string]*archive)
	return nil
}

func (fs *zipfs) ListStorageSpaces(_ context.Context, _ []*provider.ListStorageSpacesRequest_Filter, _ bool) ([]*provider.StorageSpace, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	spaces := make([]*provider.StorageSpace, 0, len(fs.archives))
	for _, a := range fs.archives {
		spaces = append(spaces, &provider.StorageSpace{
			Id:        &provider.StorageSpaceId{OpaqueId: a.spaceID},
			SpaceType: "archive",
			Name:      path.Base(a.zipPath),
			Mtime:     toTimestamp(a.modified),
			Root: &provider.ResourceId{
				StorageId: a.spaceID,
				SpaceId:   a.spaceID,
				OpaqueId:  a.spaceID,
			},
		})
	}
	return spaces, nil
}

func (fs *zipfs) GetQuota(_ context.Context, _ *provider.Reference) (uint64, uint64, uint64, error) {
	return 0, 0, 0, nil // read-only, no quota
}

func (fs *zipfs) GetMD(_ context.Context, ref *provider.Reference, _, _ []string) (*provider.ResourceInfo, error) {
	a, err := fs.getArchive(ref.GetResourceId().GetSpaceId())
	if err != nil {
		return nil, err
	}

	p := normPath(ref.GetPath())
	if p == "" || p == "/" || p == "." {
		// Root of the archive
		return fs.rootInfo(a), nil
	}

	// Find the entry in the ZIP
	entry := fs.findEntry(a, p)
	if entry == nil {
		// Maybe it's a directory (implicit in ZIP)
		if fs.isImplicitDir(a, p) {
			return fs.dirInfo(a, p), nil
		}
		return nil, errtypes.NotFound(p)
	}

	return fs.fileInfo(a, entry), nil
}

func (fs *zipfs) ListFolder(_ context.Context, ref *provider.Reference, _, _ []string) ([]*provider.ResourceInfo, error) {
	a, err := fs.getArchive(ref.GetResourceId().GetSpaceId())
	if err != nil {
		return nil, err
	}

	p := normPath(ref.GetPath())
	prefix := ""
	if p != "" && p != "/" && p != "." {
		prefix = strings.TrimPrefix(p, "/") + "/"
	}

	seen := make(map[string]bool)
	var result []*provider.ResourceInfo

	for _, f := range a.reader.File {
		name := f.Name
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel == "" {
			continue
		}

		// Direct child?
		parts := strings.SplitN(rel, "/", 2)
		childName := parts[0]
		if seen[childName] {
			continue
		}
		seen[childName] = true

		if len(parts) > 1 || strings.HasSuffix(name, "/") {
			// It's a subdirectory
			result = append(result, fs.dirInfo(a, prefix+childName))
		} else {
			result = append(result, fs.fileInfo(a, f))
		}
	}

	return result, nil
}

func (fs *zipfs) Download(_ context.Context, ref *provider.Reference, _ func(*provider.ResourceInfo) bool) (*provider.ResourceInfo, io.ReadCloser, error) {
	a, err := fs.getArchive(ref.GetResourceId().GetSpaceId())
	if err != nil {
		return nil, nil, err
	}

	p := normPath(ref.GetPath())
	entry := fs.findEntry(a, p)
	if entry == nil {
		return nil, nil, errtypes.NotFound(p)
	}

	rc, err := entry.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("open zip entry %s: %w", p, err)
	}

	return fs.fileInfo(a, entry), rc, nil
}

func (fs *zipfs) GetPathByID(_ context.Context, id *provider.ResourceId) (string, error) {
	return id.GetOpaqueId(), nil
}

// --- Write operations: all return NotSupported ---

func (fs *zipfs) CreateReference(_ context.Context, _ string, _ *url.URL) error {
	return errtypes.NotSupported("zipfs is read-only")
}
func (fs *zipfs) CreateDir(_ context.Context, _ *provider.Reference) error {
	return errtypes.NotSupported("zipfs is read-only")
}
func (fs *zipfs) TouchFile(_ context.Context, _ *provider.Reference, _ bool, _ string) error {
	return errtypes.NotSupported("zipfs is read-only")
}
func (fs *zipfs) Delete(_ context.Context, _ *provider.Reference) error {
	return errtypes.NotSupported("zipfs is read-only")
}
func (fs *zipfs) Move(_ context.Context, _, _ *provider.Reference) error {
	return errtypes.NotSupported("zipfs is read-only")
}
func (fs *zipfs) InitiateUpload(_ context.Context, _ *provider.Reference, _ int64, _ map[string]string) (map[string]string, error) {
	return nil, errtypes.NotSupported("zipfs is read-only")
}
func (fs *zipfs) Upload(_ context.Context, _ storage.UploadRequest, _ storage.UploadFinishedFunc) (*provider.ResourceInfo, error) {
	return nil, errtypes.NotSupported("zipfs is read-only")
}

// --- Helpers ---

func (fs *zipfs) findEntry(a *archive, p string) *zip.File {
	clean := strings.TrimPrefix(p, "/")
	for _, f := range a.reader.File {
		if f.Name == clean || f.Name == clean+"/" {
			if !strings.HasSuffix(f.Name, "/") {
				return f
			}
		}
	}
	return nil
}

func (fs *zipfs) isImplicitDir(a *archive, p string) bool {
	prefix := strings.TrimPrefix(p, "/") + "/"
	for _, f := range a.reader.File {
		if strings.HasPrefix(f.Name, prefix) {
			return true
		}
	}
	return false
}

func (fs *zipfs) rootInfo(a *archive) *provider.ResourceInfo {
	return &provider.ResourceInfo{
		Type: provider.ResourceType_RESOURCE_TYPE_CONTAINER,
		Id: &provider.ResourceId{
			StorageId: a.spaceID,
			SpaceId:   a.spaceID,
			OpaqueId:  a.spaceID,
		},
		Path:  "/",
		Size:  uint64(a.size),
		Mtime: toTimestamp(a.modified),
		Name:  path.Base(a.zipPath),
		Space: &provider.StorageSpace{
			SpaceType: "archive",
		},
	}
}

func (fs *zipfs) dirInfo(a *archive, p string) *provider.ResourceInfo {
	name := path.Base(p)
	h := md5.Sum([]byte(p))
	return &provider.ResourceInfo{
		Type: provider.ResourceType_RESOURCE_TYPE_CONTAINER,
		Id: &provider.ResourceId{
			StorageId: a.spaceID,
			SpaceId:   a.spaceID,
			OpaqueId:  fmt.Sprintf("%x", h),
		},
		Path:  "/" + strings.TrimPrefix(p, "/"),
		Name:  name,
		Mtime: toTimestamp(a.modified),
	}
}

func (fs *zipfs) fileInfo(a *archive, f *zip.File) *provider.ResourceInfo {
	name := path.Base(f.Name)
	h := md5.Sum([]byte(f.Name))
	_ = bytes.Compare(nil, nil) // keep bytes import
	return &provider.ResourceInfo{
		Type: provider.ResourceType_RESOURCE_TYPE_FILE,
		Id: &provider.ResourceId{
			StorageId: a.spaceID,
			SpaceId:   a.spaceID,
			OpaqueId:  fmt.Sprintf("%x", h),
		},
		Path:     "/" + f.Name,
		Name:     name,
		Size:     f.UncompressedSize64,
		Mtime:    toTimestamp(f.Modified),
		Checksum: &provider.ResourceChecksum{Type: provider.ResourceChecksumType_RESOURCE_CHECKSUM_TYPE_CRC32, Sum: fmt.Sprintf("%08x", f.CRC32)},
	}
}

func normPath(p string) string {
	p = path.Clean(p)
	if p == "." {
		return "/"
	}
	return p
}

func toTimestamp(t time.Time) *typespb.Timestamp {
	return &typespb.Timestamp{
		Seconds: uint64(t.Unix()),
		Nanos:   uint32(t.Nanosecond()),
	}
}
