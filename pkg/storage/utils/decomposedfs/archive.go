package decomposedfs

import (
	"context"
	"io"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"

	"github.com/opencloud-eu/reva/v2/pkg/storage/fs/zipfs"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/node"
)

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

// statArchiveEntry returns metadata for a path inside an archive.
func (fs *Decomposedfs) statArchiveEntry(ctx context.Context, n *node.Node, innerPath string) (*provider.ResourceInfo, error) {
	diskPath := n.InternalPath()

	archive, err := zipfs.GlobalCache().Get(diskPath)
	if err != nil {
		return nil, err
	}

	return zipfs.Stat(archive, innerPath, n.SpaceID)
}

// downloadArchiveEntry streams a single file from inside an archive.
func (fs *Decomposedfs) downloadArchiveEntry(ctx context.Context, n *node.Node, innerPath string) (*provider.ResourceInfo, io.ReadCloser, error) {
	diskPath := n.InternalPath()

	archive, err := zipfs.GlobalCache().Get(diskPath)
	if err != nil {
		return nil, nil, err
	}

	spaceID := n.SpaceID
	return zipfs.Download(archive, innerPath, spaceID)
}
