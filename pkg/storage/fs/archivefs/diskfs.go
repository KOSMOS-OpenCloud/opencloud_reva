package archivefs

import (
	"fmt"
	"io"
	"io/fs"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
)

func init() {
	RegisterOpener(&diskfsOpener{})
}

// diskfsOpener handles ISO9660, FAT, ext4, squashfs via go-diskfs.
type diskfsOpener struct{}

var diskfsExtensions = []string{".iso", ".img", ".raw", ".squashfs", ".fat", ".ext4"}

func (d *diskfsOpener) CanHandle(name string) bool {
	lower := strings.ToLower(name)
	for _, f := range d.Formats() {
		if strings.HasSuffix(lower, f.Extension) {
			return true
		}
	}
	return false
}

func (d *diskfsOpener) Formats() []ArchiveFormat {
	return []ArchiveFormat{
		{Extension: ".iso", MimeTypes: []string{"application/x-iso9660-image"}},
		{Extension: ".img", MimeTypes: []string{"application/x-raw-disk-image", "application/octet-stream"}},
		{Extension: ".raw", MimeTypes: []string{"application/x-raw-disk-image", "application/octet-stream"}},
		{Extension: ".squashfs", MimeTypes: []string{"application/octet-stream"}},
		{Extension: ".fat", MimeTypes: []string{"application/octet-stream"}},
		{Extension: ".ext4", MimeTypes: []string{"application/octet-stream"}},
	}
}

func (d *diskfsOpener) Open(diskPath string) (fs.FS, io.Closer, error) {
	dsk, err := diskfs.Open(diskPath, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		return nil, nil, fmt.Errorf("diskfs open %s: %w", diskPath, err)
	}

	// Try to get the filesystem — for partitioned disks, try partition 1 first
	fsys, err := d.getFS(dsk)
	if err != nil {
		return nil, nil, fmt.Errorf("diskfs get filesystem %s: %w", diskPath, err)
	}

	// go-diskfs FileSystem implements fs.FS, fs.ReadDirFS, fs.StatFS
	return fsys, &diskCloser{dsk}, nil
}

func (d *diskfsOpener) getFS(dsk *disk.Disk) (fs.FS, error) {
	// Try whole-disk filesystem first (ISO, squashfs)
	fsys, err := dsk.GetFilesystem(0)
	if err == nil {
		return fsys, nil
	}

	// Try partition 1 (common for disk images with partition table)
	fsys, err = dsk.GetFilesystem(1)
	if err == nil {
		return fsys, nil
	}

	return nil, fmt.Errorf("no readable filesystem found")
}

type diskCloser struct {
	dsk *disk.Disk
}

func (dc *diskCloser) Close() error {
	if dc.dsk != nil && dc.dsk.Backend != nil {
		return dc.dsk.Backend.Close()
	}
	return nil
}
