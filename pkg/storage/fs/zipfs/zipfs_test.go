package zipfs

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathSplit(t *testing.T) {
	tests := []struct {
		input     string
		zipPath   string
		innerPath string
		found     bool
	}{
		{"/docs/archive.zip/readme.md", "/docs/archive.zip", "readme.md", true},
		{"/docs/archive.zip/sub/file.txt", "/docs/archive.zip", "sub/file.txt", true},
		{"/docs/archive.zip/", "/docs/archive.zip", "", true},
		{"/docs/archive.zip", "", "", false},       // no slash after .zip = normal file
		{"/docs/report.txt", "", "", false},         // not a zip
		{"/a.zip/b.zip/c.txt", "/a.zip", "b.zip/c.txt", true}, // first .zip/ wins
		{"archive.zip/file", "archive.zip", "file", true},
		{"/UPPER.ZIP/file", "/UPPER.ZIP", "file", true}, // case insensitive
	}

	for _, tt := range tests {
		zp, ip, found := PathSplit(tt.input)
		if found != tt.found || zp != tt.zipPath || ip != tt.innerPath {
			t.Errorf("PathSplit(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, zp, ip, found, tt.zipPath, tt.innerPath, tt.found)
		}
	}
}

func TestIsFile(t *testing.T) {
	// Create a temp file
	f, err := os.CreateTemp("", "zipfs-test-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()

	if !IsFile(f.Name()) {
		t.Error("expected IsFile=true for temp file")
	}

	// Create a temp dir
	dir, err := os.MkdirTemp("", "zipfs-test-dir-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	if IsFile(dir) {
		t.Error("expected IsFile=false for directory named .zip")
	}

	if IsFile("/nonexistent/path.zip") {
		t.Error("expected IsFile=false for nonexistent path")
	}
}

func createTestZip(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(content))
	}
	w.Close()
	return zipPath
}

func TestCacheAndListFolder(t *testing.T) {
	zipPath := createTestZip(t, map[string]string{
		"readme.md":       "# Hello",
		"docs/guide.txt":  "guide content",
		"docs/faq.txt":    "faq content",
		"src/main.go":     "package main",
		"src/lib/util.go": "package lib",
	})

	cache := NewCache(0)
	defer cache.Close()

	a, err := cache.Get(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	// List root
	infos, err := ListFolder(a, "", "test-space", "archive-node-1")
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, info := range infos {
		names[info.Name] = true
	}
	if !names["readme.md"] || !names["docs"] || !names["src"] {
		t.Errorf("root listing missing entries: %v", names)
	}

	// List docs/
	infos, err = ListFolder(a, "docs", "test-space", "archive-node-1")
	if err != nil {
		t.Fatal(err)
	}
	names = make(map[string]bool)
	for _, info := range infos {
		names[info.Name] = true
	}
	if !names["guide.txt"] || !names["faq.txt"] {
		t.Errorf("docs/ listing missing entries: %v", names)
	}
}

func TestStatAndDownload(t *testing.T) {
	zipPath := createTestZip(t, map[string]string{
		"hello.txt":      "hello world",
		"sub/nested.txt": "nested content",
	})

	cache := NewCache(0)
	defer cache.Close()

	a, err := cache.Get(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	// Stat file
	info, err := Stat(a, "hello.txt", "test-space", "archive-node-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "hello.txt" {
		t.Errorf("Stat name = %q, want hello.txt", info.Name)
	}

	// Stat directory
	info, err = Stat(a, "sub", "test-space", "archive-node-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "sub" {
		t.Errorf("Stat dir name = %q, want sub", info.Name)
	}

	// Download
	info, rc, err := Download(a, "hello.txt", "test-space", "archive-node-1")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	var buf bytes.Buffer
	buf.ReadFrom(rc)
	if buf.String() != "hello world" {
		t.Errorf("Download content = %q, want 'hello world'", buf.String())
	}

	// Not found
	_, err = Stat(a, "nonexistent", "test-space", "archive-node-1")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestIDConsistency(t *testing.T) {
	// IDs from ListFolder must match IDs from Stat for the same entry
	zipPath := createTestZip(t, map[string]string{
		"dir1/dir2/file.txt": "content",
		"dir1/other.txt":     "other",
		"root.txt":           "root",
	})

	cache := NewCache(0)
	defer cache.Close()

	a, err := cache.Get(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	spaceID := "test-space"
	archiveNodeID := "archive-node-1"

	// List root → get dir1's ID
	rootItems, err := ListFolder(a, "", spaceID, archiveNodeID)
	if err != nil {
		t.Fatal(err)
	}
	var dir1ListID string
	for _, item := range rootItems {
		if item.Name == "dir1" {
			dir1ListID = item.Id.OpaqueId
		}
	}
	if dir1ListID == "" {
		t.Fatal("dir1 not found in root listing")
	}

	// Stat dir1 → get dir1's ID
	dir1Stat, err := Stat(a, "dir1", spaceID, archiveNodeID)
	if err != nil {
		t.Fatal(err)
	}

	if dir1ListID != dir1Stat.Id.OpaqueId {
		t.Errorf("ID mismatch for dir1: ListFolder=%q, Stat=%q", dir1ListID, dir1Stat.Id.OpaqueId)
	}

	// List dir1 → get dir2's ID
	dir1Items, err := ListFolder(a, "dir1", spaceID, archiveNodeID)
	if err != nil {
		t.Fatal(err)
	}
	var dir2ListID, otherListID string
	for _, item := range dir1Items {
		switch item.Name {
		case "dir2":
			dir2ListID = item.Id.OpaqueId
		case "other.txt":
			otherListID = item.Id.OpaqueId
		}
	}
	if dir2ListID == "" {
		t.Fatal("dir2 not found in dir1 listing")
	}

	// Stat dir1/dir2 → must match
	dir2Stat, err := Stat(a, "dir1/dir2", spaceID, archiveNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if dir2ListID != dir2Stat.Id.OpaqueId {
		t.Errorf("ID mismatch for dir1/dir2: ListFolder=%q, Stat=%q", dir2ListID, dir2Stat.Id.OpaqueId)
	}

	// Stat dir1/other.txt → must match
	otherStat, err := Stat(a, "dir1/other.txt", spaceID, archiveNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if otherListID != otherStat.Id.OpaqueId {
		t.Errorf("ID mismatch for dir1/other.txt: ListFolder=%q, Stat=%q", otherListID, otherStat.Id.OpaqueId)
	}

	// Name must be base name only
	if dir2Stat.Name != "dir2" {
		t.Errorf("Stat name for dir1/dir2 = %q, want 'dir2'", dir2Stat.Name)
	}
	if otherStat.Name != "other.txt" {
		t.Errorf("Stat name for dir1/other.txt = %q, want 'other.txt'", otherStat.Name)
	}
}

func TestReloadRoundtrip(t *testing.T) {
	// Simulates page reload: ListFolder → take fileId → ParseArchiveID → Stat
	// The fileId from a listing must be parseable back to archiveNodeID + innerPath,
	// and Stat with that innerPath must return matching data.
	zipPath := createTestZip(t, map[string]string{
		"dir1/dir2/file.txt": "deep content",
		"dir1/other.txt":     "other",
	})

	cache := NewCache(0)
	defer cache.Close()

	a, err := cache.Get(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	spaceID := "test-space"
	archiveNodeID := "real-node-uuid-123"

	// Step 1: ListFolder dir1 — get dir2's fileId (as browser would store it)
	items, err := ListFolder(a, "dir1", spaceID, archiveNodeID)
	if err != nil {
		t.Fatal(err)
	}
	var dir2FileID string
	for _, item := range items {
		if item.Name == "dir2" {
			dir2FileID = item.Id.OpaqueId
		}
	}
	if dir2FileID == "" {
		t.Fatal("dir2 not found in listing")
	}

	// Step 2: Simulate reload — parse the fileId back
	parsedNodeID, parsedInnerPath, ok := ParseArchiveID(dir2FileID)
	if !ok {
		t.Fatalf("ParseArchiveID(%q) failed", dir2FileID)
	}
	if parsedNodeID != archiveNodeID {
		t.Errorf("parsed archiveNodeID = %q, want %q", parsedNodeID, archiveNodeID)
	}
	if parsedInnerPath != "dir1/dir2" {
		t.Errorf("parsed innerPath = %q, want 'dir1/dir2'", parsedInnerPath)
	}

	// Step 3: Stat with parsed innerPath — must return valid dir info
	statInfo, err := Stat(a, parsedInnerPath, spaceID, archiveNodeID)
	if err != nil {
		t.Fatalf("Stat(%q) failed: %v", parsedInnerPath, err)
	}
	if statInfo.Name != "dir2" {
		t.Errorf("Stat name = %q, want 'dir2'", statInfo.Name)
	}
	// The Stat fileId must match the original listing fileId
	if statInfo.Id.OpaqueId != dir2FileID {
		t.Errorf("Stat fileId = %q, ListFolder fileId = %q — mismatch!", statInfo.Id.OpaqueId, dir2FileID)
	}

	// Step 4: fileId must be URL-safe (no slashes)
	if strings.Contains(dir2FileID, "/") {
		t.Errorf("fileId contains slash (not URL-safe): %q", dir2FileID)
	}
}

func TestParseArchiveID(t *testing.T) {
	tests := []struct {
		input         string
		archiveNodeID string
		innerPath     string
		ok            bool
	}{
		{"abc123!arc.bWlhdTIvd2F1d2F1Mg", "abc123", "miau2/wauwau2", true},
		{"abc123!arc", "abc123", "", true},
		{"abc123!arc.ZmlsZS50eHQ", "abc123", "file.txt", true},
		{"abc123", "", "", false},              // no !arc
		{"abc123!other", "", "", false},         // wrong marker
		{"abc123!arcpy", "", "", false},         // !arcpy != !arc
	}

	for _, tt := range tests {
		nodeID, innerPath, ok := ParseArchiveID(tt.input)
		if ok != tt.ok || nodeID != tt.archiveNodeID || innerPath != tt.innerPath {
			t.Errorf("ParseArchiveID(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, nodeID, innerPath, ok, tt.archiveNodeID, tt.innerPath, tt.ok)
		}
	}
}
