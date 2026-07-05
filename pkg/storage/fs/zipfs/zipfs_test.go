package zipfs

import (
	"testing"
)

// Tests for the zipfs compatibility wrapper.
// Full tests are in pkg/storage/fs/archivefs/archivefs_test.go

func TestWrapperListFolder(t *testing.T) {
	// Verify the wrapper delegates correctly
	if GlobalCache == nil {
		t.Error("GlobalCache is nil")
	}
	if ListFolder == nil {
		t.Error("ListFolder is nil")
	}
	if Stat == nil {
		t.Error("Stat is nil")
	}
	if Download == nil {
		t.Error("Download is nil")
	}
	if IsArchiveName == nil {
		t.Error("IsArchiveName is nil")
	}
}

func TestWrapperParseArchiveID(t *testing.T) {
	nodeID, innerPath, ok := ParseArchiveID("abc!arc.dGVzdA")
	if !ok || nodeID != "abc" || innerPath != "test" {
		t.Errorf("ParseArchiveID failed: (%q, %q, %v)", nodeID, innerPath, ok)
	}
}
