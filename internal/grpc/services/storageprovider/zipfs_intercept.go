package storageprovider

import (
	"context"
	"path/filepath"
	"strings"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"

	"github.com/opencloud-eu/reva/v2/pkg/appctx"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/status"
	"github.com/opencloud-eu/reva/v2/pkg/storage/fs/zipfs"
)

// zipCache uses the shared global cache.
var zipCache = zipfs.GlobalCache()

// tryZipStat handles Stat requests for paths inside ZIP archives.
// Returns (response, handled). If handled=false, the caller should
// proceed with normal stat logic.
func (s *Service) tryZipStat(ctx context.Context, ref *provider.Reference) (*provider.StatResponse, bool) {
	if ref == nil || ref.Path == "" {
		return nil, false
	}

	zipPath, innerPath, found := zipfs.PathSplit(ref.Path)
	if !found {
		return nil, false
	}

	// Resolve the ZIP file through the normal storage layer
	zipRef := &provider.Reference{
		ResourceId: ref.ResourceId,
		Path:       zipPath,
	}
	md, err := s.Storage.GetMD(ctx, zipRef, nil, nil)
	if err != nil {
		return nil, false // ZIP file not found → fall through to normal handling
	}

	// Must be a file, not a directory named .zip
	if md.Type != provider.ResourceType_RESOURCE_TYPE_FILE {
		return nil, false
	}

	// Get the on-disk path for the ZIP file
	diskPath := getDiskPath(ctx, s, zipRef)
	if diskPath == "" || !zipfs.IsFile(diskPath) {
		return nil, false
	}

	archive, err := zipCache.Get(diskPath)
	if err != nil {
		appctx.GetLogger(ctx).Warn().Err(err).Str("zip", diskPath).Msg("zipfs: failed to open archive")
		return nil, false
	}

	spaceID := ref.GetResourceId().GetSpaceId()
	info, err := zipfs.Stat(archive, innerPath, spaceID)
	if err != nil {
		return &provider.StatResponse{
			Status: status.NewStatusFromErrType(ctx, "zipfs stat", err),
		}, true
	}

	return &provider.StatResponse{
		Status: status.NewOK(ctx),
		Info:   info,
	}, true
}

// tryZipListContainer handles ListContainer requests for paths inside ZIP archives.
func (s *Service) tryZipListContainer(ctx context.Context, ref *provider.Reference) (*provider.ListContainerResponse, bool) {
	if ref == nil || ref.Path == "" {
		return nil, false
	}

	zipPath, innerPath, found := zipfs.PathSplit(ref.Path)
	if !found {
		return nil, false
	}

	zipRef := &provider.Reference{
		ResourceId: ref.ResourceId,
		Path:       zipPath,
	}
	md, err := s.Storage.GetMD(ctx, zipRef, nil, nil)
	if err != nil || md.Type != provider.ResourceType_RESOURCE_TYPE_FILE {
		return nil, false
	}

	diskPath := getDiskPath(ctx, s, zipRef)
	if diskPath == "" || !zipfs.IsFile(diskPath) {
		return nil, false
	}

	archive, err := zipCache.Get(diskPath)
	if err != nil {
		return nil, false
	}

	spaceID := ref.GetResourceId().GetSpaceId()
	infos, err := zipfs.ListFolder(archive, innerPath, spaceID)
	if err != nil {
		return &provider.ListContainerResponse{
			Status: status.NewStatusFromErrType(ctx, "zipfs list", err),
		}, true
	}

	return &provider.ListContainerResponse{
		Status: status.NewOK(ctx),
		Infos:  infos,
	}, true
}

// InternalPather is an optional interface for storage drivers that can
// resolve a reference to an on-disk file path (e.g. posixfs).
type InternalPather interface {
	InternalPath(ctx context.Context, ref *provider.Reference) (string, error)
}

// getDiskPath resolves a reference to an on-disk file path.
// Works for posixfs (which implements InternalPather or has direct paths).
// Returns "" for storage drivers that don't expose file paths (e.g. decomposedfs with S3 blobs).
func getDiskPath(ctx context.Context, s *Service, ref *provider.Reference) string {
	// Check if the storage driver can give us a direct path
	if ip, ok := s.Storage.(InternalPather); ok {
		p, err := ip.InternalPath(ctx, ref)
		if err == nil {
			return p
		}
	}

	// Fallback: try GetMD and check if the resource has an opaque internal path
	md, err := s.Storage.GetMD(ctx, ref, nil, nil)
	if err != nil {
		return ""
	}

	// Check opaque for internal path hint (posixfs sets this)
	if md.GetOpaque() != nil {
		if entry, ok := md.GetOpaque().GetMap()["internal-path"]; ok {
			return string(entry.GetValue())
		}
	}

	return ""
}
