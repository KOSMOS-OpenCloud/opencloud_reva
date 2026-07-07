package archivefs

import (
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/bodgit/sevenzip"
)

func init() {
	RegisterOpener(&sevenzOpener{})
}

type sevenzOpener struct{}

func (s *sevenzOpener) CanHandle(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".7z")
}

func (s *sevenzOpener) Open(diskPath string) (fs.FS, io.Closer, error) {
	r, err := sevenzip.OpenReader(diskPath)
	if err != nil {
		return nil, nil, err
	}
	return &sevenzFS{r: r}, r, nil
}

// sevenzFS wraps sevenzip.Reader to implement fs.FS
type sevenzFS struct {
	r *sevenzip.ReadCloser
}

func (s *sevenzFS) Open(name string) (fs.File, error) {
	// Normalize: fs.FS expects forward slashes, no leading slash
	name = strings.TrimPrefix(name, "/")
	if name == "." || name == "" {
		return &sevenzDir{name: ".", entries: s.rootEntries()}, nil
	}

	for _, f := range s.r.File {
		fname := strings.TrimPrefix(f.Name, "/")
		fname = strings.TrimSuffix(fname, "/")
		if fname == name {
			if f.FileInfo().IsDir() {
				return &sevenzDir{name: fname, entries: s.dirEntries(fname)}, nil
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			return &sevenzFile{ReadCloser: rc, info: f.FileInfo()}, nil
		}
	}

	// Check if it's a directory (some 7z files don't have explicit dir entries)
	prefix := name + "/"
	for _, f := range s.r.File {
		if strings.HasPrefix(f.Name, prefix) {
			return &sevenzDir{name: name, entries: s.dirEntries(name)}, nil
		}
	}

	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (s *sevenzFS) rootEntries() []fs.DirEntry {
	seen := make(map[string]bool)
	var entries []fs.DirEntry
	for _, f := range s.r.File {
		name := strings.TrimPrefix(f.Name, "/")
		parts := strings.SplitN(name, "/", 2)
		top := parts[0]
		if top == "" || seen[top] {
			continue
		}
		seen[top] = true
		isDir := len(parts) > 1 || f.FileInfo().IsDir()
		entries = append(entries, &sevenzDirEntry{name: top, isDir: isDir, info: f.FileInfo()})
	}
	return entries
}

func (s *sevenzFS) dirEntries(dir string) []fs.DirEntry {
	prefix := dir + "/"
	seen := make(map[string]bool)
	var entries []fs.DirEntry
	for _, f := range s.r.File {
		fname := strings.TrimPrefix(f.Name, "/")
		if !strings.HasPrefix(fname, prefix) {
			continue
		}
		rest := fname[len(prefix):]
		parts := strings.SplitN(rest, "/", 2)
		child := parts[0]
		if child == "" || seen[child] {
			continue
		}
		seen[child] = true
		isDir := len(parts) > 1 || f.FileInfo().IsDir()
		entries = append(entries, &sevenzDirEntry{name: child, isDir: isDir, info: f.FileInfo()})
	}
	return entries
}

type sevenzFile struct {
	io.ReadCloser
	info fs.FileInfo
}

func (f *sevenzFile) Stat() (fs.FileInfo, error) { return f.info, nil }

type sevenzDir struct {
	name    string
	entries []fs.DirEntry
	offset  int
}

func (d *sevenzDir) Read([]byte) (int, error)              { return 0, &fs.PathError{Op: "read", Path: d.name, Err: fs.ErrInvalid} }
func (d *sevenzDir) Close() error                          { return nil }
func (d *sevenzDir) Stat() (fs.FileInfo, error)            { return &dirInfo{name: d.name}, nil }
func (d *sevenzDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		entries := d.entries[d.offset:]
		d.offset = len(d.entries)
		return entries, nil
	}
	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	entries := d.entries[d.offset:end]
	d.offset = end
	if d.offset >= len(d.entries) {
		return entries, io.EOF
	}
	return entries, nil
}

type sevenzDirEntry struct {
	name  string
	isDir bool
	info  fs.FileInfo
}

func (e *sevenzDirEntry) Name() string               { return e.name }
func (e *sevenzDirEntry) IsDir() bool                { return e.isDir }
func (e *sevenzDirEntry) Type() fs.FileMode          { if e.isDir { return fs.ModeDir }; return 0 }
func (e *sevenzDirEntry) Info() (fs.FileInfo, error)  { return e.info, nil }

type dirInfo struct{ name string }

func (d *dirInfo) Name() string      { return d.name }
func (d *dirInfo) Size() int64       { return 0 }
func (d *dirInfo) Mode() fs.FileMode { return fs.ModeDir | 0o555 }
func (d *dirInfo) IsDir() bool       { return true }
func (d *dirInfo) Sys() any            { return nil }
func (d *dirInfo) ModTime() time.Time  { return time.Time{} }
