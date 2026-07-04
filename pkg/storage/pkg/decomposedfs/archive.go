package decomposedfs

import (
	"context"
	"strings"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"

	"github.com/opencloud-eu/reva/v2/pkg/storage/fs/zipfs"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/node"
)

// archiveMimeTypes lists MIME types that can be browsed as virtual directories.
var archiveMimeTypes = map[string]bool{
	"application/zip":              true,
	"application/x-zip-compressed": true,
}

// isArchiveNode checks if a node is a file with a browsable archive MIME type.
func isArchiveNode(ctx context.Context, n *node.Node) bool {
	if n == nil || !n.Exists || n.IsDir(ctx) {
		return false
	}
	mime := n.MimeType(ctx)
	return archiveMimeTypes[strings.ToLower(mime)]
}

// listArchiveContents lists the contents of an archive file as if it were a directory.
func (fs *Decomposedfs) listArchiveContents(ctx context.Context, n *node.Node, innerPath string) ([]*provider.ResourceInfo, error) {
	diskPath := n.InternalPath()

	archive, err := zipfs.GlobalCache().Get(diskPath)
	if err != nil {
		return nil, err
	}

	spaceID := n.SpaceID
	return zipfs.ListFolder(archive, innerPath, spaceID)
}

// downloadArchiveEntry streams a single file from inside an archive.
func (fs *Decomposedfs) downloadArchiveEntry(ctx context.Context, n *node.Node, innerPath string) (*provider.ResourceInfo, ReadCloserWithInfo, error) {
	diskPath := n.InternalPath()

	archive, err := zipfs.GlobalCache().Get(diskPath)
	if err != nil {
		return nil, nil, err
	}

	spaceID := n.SpaceID
	info, rc, err := zipfs.Download(archive, innerPath, spaceID)
	if err != nil {
		return nil, nil, err
	}

	return info, rc, nil
}

// ReadCloserWithInfo is a type alias for io.ReadCloser used in Download returns.
type ReadCloserWithInfo = interface {
	Read(p []byte) (n int, err error)
	Close() error
}
