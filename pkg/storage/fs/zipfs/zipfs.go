// Package zipfs is a compatibility wrapper around archivefs.
// New code should import archivefs directly.
package zipfs

import (
	"github.com/opencloud-eu/reva/v2/pkg/storage/fs/archivefs"
)

// Re-export all public symbols from archivefs for backward compatibility.
var (
	GlobalCache   = archivefs.GlobalCache
	NewCache      = archivefs.NewCache
	ListFolder    = archivefs.ListFolder
	Stat          = archivefs.Stat
	Download      = archivefs.Download
	IsArchiveName = archivefs.IsArchiveName
)

type CachedArchive = archivefs.CachedArchive
type Cache = archivefs.Cache

func ParseArchiveID(opaqueID string) (archiveNodeID, innerPath string, ok bool) {
	return archivefs.ParseArchiveID(opaqueID)
}
