package posix

import (
	"context"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/opencloud-eu/reva/v2/pkg/errtypes"
)

// InternalPath resolves a reference to the on-disk file path.
// For posixfs, this returns the actual filesystem path.
func (fs *posixFS) InternalPath(ctx context.Context, ref *provider.Reference) (string, error) {
	spaceID := ref.GetResourceId().GetSpaceId()
	if spaceID == "" {
		return "", errtypes.NotFound("no space ID")
	}

	// Get the resource info to find the node ID
	md, err := fs.FS.GetMD(ctx, ref, nil, nil)
	if err != nil {
		return "", err
	}

	nodeID := md.GetId().GetOpaqueId()
	if nodeID == "" {
		return "", errtypes.NotFound("no node ID")
	}

	return fs.tree.InternalPath(spaceID, nodeID), nil
}
