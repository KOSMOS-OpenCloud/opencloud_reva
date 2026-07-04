package propfind

import (
	"fmt"
	"html"
	"net/http"
	"path"
	"strconv"
	"time"

	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"

	"github.com/opencloud-eu/reva/v2/internal/http/services/owncloud/ocdav/spacelookup"
	"github.com/opencloud-eu/reva/v2/pkg/appctx"
	"github.com/opencloud-eu/reva/v2/pkg/storage/fs/zipfs"
)

// tryZipPropfind intercepts PROPFIND for paths inside ZIP archives.
// Returns true if the request was handled.
func (p *Handler) tryZipPropfind(w http.ResponseWriter, r *http.Request, spaceID string) bool {
	ctx := r.Context()
	log := appctx.GetLogger(ctx)

	log.Info().Str("url_path", r.URL.Path).Str("spaceID", spaceID).Msg("zipfs: checking PROPFIND path")

	// Two cases:
	// 1. Path contains .zip/subpath → browse inside archive
	// 2. Path ends with .zip + Depth > 0 → browse archive root
	//    (trailing slash is stripped by path.Clean, so we can't rely on it)
	zipPath, innerPath, found := zipfs.PathSplit(r.URL.Path)
	if !found {
		// Case 2: path ends with archive extension, Depth > 0 = listing intent
		depth := r.Header.Get("Depth")
		if depth != "0" && zipfs.LooksLikeArchive(r.URL.Path) {
			zipPath = r.URL.Path
			innerPath = ""
			found = true
		}
	}
	if !found {
		return false
	}

	log.Warn().Str("zip_path", zipPath).Str("inner_path", innerPath).Msg("zipfs: PROPFIND intercepted")

	// Stat the ZIP file itself
	zipRef, err := spacelookup.MakeStorageSpaceReference(spaceID, zipPath)
	if err != nil {
		return false
	}

	client, err := p.selector.Next()
	if err != nil {
		return false
	}

	sRes, err := client.Stat(ctx, &provider.StatRequest{Ref: &zipRef})
	if err != nil || sRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return false
	}

	// Must be a file, not a directory named .zip
	if sRes.GetInfo().GetType() != provider.ResourceType_RESOURCE_TYPE_FILE {
		return false
	}

	// Resolve disk path — we need the actual file to open it
	pathRes, err := client.GetPath(ctx, &provider.GetPathRequest{
		ResourceId: sRes.GetInfo().GetId(),
	})
	if err != nil || pathRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		log.Debug().Msg("zipfs: cannot resolve disk path for PROPFIND")
		return false
	}

	diskPath := pathRes.GetPath()
	if !zipfs.IsFile(diskPath) {
		return false
	}

	archive, err := zipfs.GlobalCache().Get(diskPath)
	if err != nil {
		log.Warn().Err(err).Str("zip", diskPath).Msg("zipfs: failed to open archive")
		return false
	}

	spaceIDStr := sRes.GetInfo().GetId().GetSpaceId()

	// Build the base href for responses
	baseHref := path.Join("/dav/spaces", spaceID, zipPath)

	// Get entries
	infos, err := zipfs.ListFolder(archive, innerPath, spaceIDStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return true
	}

	// Also stat the current directory for the self entry
	selfInfo, _ := zipfs.Stat(archive, innerPath, spaceIDStr)

	// Write multistatus response
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)

	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>`)
	fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">`)

	// Self entry
	if selfInfo != nil {
		selfHref := baseHref
		if innerPath != "" {
			selfHref = path.Join(baseHref, innerPath)
		}
		writeResponse(w, selfHref+"/", selfInfo)
	}

	// Children
	for _, info := range infos {
		childHref := path.Join(baseHref, innerPath, info.GetName())
		if info.GetType() == provider.ResourceType_RESOURCE_TYPE_CONTAINER {
			childHref += "/"
		}
		writeResponse(w, childHref, info)
	}

	fmt.Fprint(w, `</d:multistatus>`)
	return true
}

func writeResponse(w http.ResponseWriter, href string, info *provider.ResourceInfo) {
	fmt.Fprintf(w, `<d:response><d:href>%s</d:href><d:propstat><d:prop>`, html.EscapeString(href))

	fmt.Fprintf(w, `<oc:name>%s</oc:name>`, html.EscapeString(info.GetName()))
	fmt.Fprintf(w, `<d:displayname>%s</d:displayname>`, html.EscapeString(info.GetName()))

	if info.GetType() == provider.ResourceType_RESOURCE_TYPE_CONTAINER {
		fmt.Fprint(w, `<d:resourcetype><d:collection/></d:resourcetype>`)
	} else {
		fmt.Fprint(w, `<d:resourcetype/>`)
		fmt.Fprintf(w, `<d:getcontentlength>%d</d:getcontentlength>`, info.GetSize())
	}

	if info.GetMtime() != nil {
		t := time.Unix(int64(info.GetMtime().GetSeconds()), 0)
		fmt.Fprintf(w, `<d:getlastmodified>%s</d:getlastmodified>`, t.UTC().Format(http.TimeFormat))
	}

	// Read-only permissions for archive contents
	fmt.Fprint(w, `<oc:permissions>R</oc:permissions>`)

	if info.GetSize() > 0 {
		fmt.Fprintf(w, `<oc:size>%s</oc:size>`, strconv.FormatUint(info.GetSize(), 10))
	}

	fmt.Fprint(w, `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
}
