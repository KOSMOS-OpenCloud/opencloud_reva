package ocdav

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"

	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"

	"github.com/opencloud-eu/reva/v2/internal/http/services/owncloud/ocdav/spacelookup"
	"github.com/opencloud-eu/reva/v2/pkg/appctx"
	"github.com/opencloud-eu/reva/v2/pkg/storage/fs/zipfs"
)

// tryZipGet intercepts GET requests for paths inside ZIP archives.
// Returns true if the request was handled (ZIP content served).
func (s *svc) tryZipGet(w http.ResponseWriter, r *http.Request, spaceID string) bool {
	zipPath, innerPath, found := zipfs.PathSplit(r.URL.Path)
	if !found || innerPath == "" {
		return false
	}

	ctx := r.Context()
	log := appctx.GetLogger(ctx)

	// Stat the ZIP file itself through the normal path
	zipRef, err := spacelookup.MakeStorageSpaceReference(spaceID, zipPath)
	if err != nil {
		return false
	}

	client, err := s.gatewaySelector.Next()
	if err != nil {
		return false
	}

	sRes, err := client.Stat(ctx, &provider.StatRequest{Ref: &zipRef})
	if err != nil || sRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return false
	}

	// Must be a file
	if sRes.GetInfo().GetType() != provider.ResourceType_RESOURCE_TYPE_FILE {
		return false
	}

	// We need the on-disk path to open the ZIP.
	// For posixfs, we can reconstruct it from the space + path.
	// Download the ZIP via the gateway and get its actual disk path.
	// TODO: for posixfs, we should get InternalPath directly.
	// For now, use the storage-provider level interceptor for Stat/ListContainer
	// and handle GET via a direct disk path lookup.

	// Try to find the file on disk via the storage provider's GetPathByID
	pathRes, err := client.GetPath(ctx, &provider.GetPathRequest{
		ResourceId: sRes.GetInfo().GetId(),
	})
	if err != nil || pathRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		log.Debug().Msg("zipfs: cannot resolve disk path for GET, falling through")
		return false
	}

	// For posixfs the path is the actual filesystem path
	diskPath := pathRes.GetPath()
	if !zipfs.IsFile(diskPath) {
		return false
	}

	archive, err := zipfs.GlobalCache().Get(diskPath)
	if err != nil {
		log.Warn().Err(err).Str("zip", diskPath).Msg("zipfs: failed to open archive for GET")
		return false
	}

	info, rc, err := zipfs.Download(archive, innerPath, spaceID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "file not found in archive: %s", innerPath)
		return true
	}
	defer rc.Close()

	// Set response headers
	w.Header().Set("Content-Type", mimeFromName(info.GetName()))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", info.GetName()))
	if info.GetSize() > 0 {
		w.Header().Set("Content-Length", strconv.FormatUint(info.GetSize(), 10))
	}
	w.Header().Set("X-Archive-Mode", "true")
	w.WriteHeader(http.StatusOK)

	io.Copy(w, rc)
	return true
}

func mimeFromName(name string) string {
	ext := filepath.Ext(name)
	if ext != "" {
		if mt := mime.TypeByExtension(ext); mt != "" {
			return mt
		}
	}
	return "application/octet-stream"
}
