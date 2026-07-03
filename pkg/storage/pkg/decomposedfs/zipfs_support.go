package decomposedfs

import (
	"context"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
)

// InternalPath resolves a reference to the on-disk file path.
// This is used by the zipfs interceptor to access ZIP files directly.
// For posixfs, this returns the actual filesystem path.
// For decomposedfs with local blobs, this returns the blob path.
func (fs *Decomposedfs) InternalPath(ctx context.Context, ref *provider.Reference) (string, error) {
	n, err := fs.lu.NodeFromResource(ctx, ref)
	if err != nil {
		return "", err
	}
	if !n.Exists {
		return "", &notFoundError{path: ref.GetPath()}
	}
	return n.InternalPath(), nil
}

type notFoundError struct {
	path string
}

func (e *notFoundError) Error() string {
	return "not found: " + e.path
}

func (e *notFoundError) IsNotFound() {}
