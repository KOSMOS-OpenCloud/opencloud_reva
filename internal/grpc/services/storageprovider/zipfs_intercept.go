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

// zipCache is a shared cache for opened ZIP archives.
var zipCache = zipfs.NewCache(0)

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

// getDiskPath resolves a reference to an on-disk file path.
// This works for posixfs where InternalPath() maps directly.
// For decomposedfs this would need the blob path — returns "" if unavailable.
func getDiskPath(ctx context.Context, s *Service, ref *provider.Reference) string {
	// Try to get the internal path via GetPathByID + root
	// For posixfs, the path is the actual filesystem path
	p, err := s.Storage.GetPathByID(ctx, ref.GetResourceId())
	if err != nil {
		return ""
	}

	// Combine with ref.Path to get the full internal path
	// The storage root is needed — we get it from the space
	fullPath := filepath.Join(p, strings.TrimPrefix(ref.Path, "/"))

	// For posixfs, this should be the actual file path
	// For decomposedfs, this won't work (returns node path, not blob path)
	// TODO: add decomposedfs support via blobstore
	return fullPath
}
