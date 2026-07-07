package archivefs

import (
	"archive/zip"
	"io"
	"io/fs"
	"os"
	"strings"
)

func init() {
	RegisterOpener(&zipOpener{})
}

type zipOpener struct{}

func (z *zipOpener) CanHandle(name string) bool {
	lower := strings.ToLower(name)
	for _, f := range z.Formats() {
		if strings.HasSuffix(lower, f.Extension) {
			return true
		}
	}
	return false
}

func (z *zipOpener) Formats() []ArchiveFormat {
	return []ArchiveFormat{
		{Extension: ".zip", MimeTypes: []string{"application/zip", "application/x-zip-compressed"}},
		{Extension: ".jar", MimeTypes: []string{"application/java-archive"}},
		{Extension: ".war", MimeTypes: []string{"application/java-archive"}},
		{Extension: ".ear", MimeTypes: []string{"application/java-archive"}},
	}
}

func (z *zipOpener) Open(diskPath string) (fs.FS, io.Closer, error) {
	f, err := os.Open(diskPath)
	if err != nil {
		return nil, nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	zr, err := zip.NewReader(f, stat.Size())
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	// zip.Reader implements fs.FS since Go 1.16
	return zr, f, nil
}
