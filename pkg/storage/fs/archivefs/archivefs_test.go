package archivefs

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestListFolder(t *testing.T) {
	zipPath := createTestZip(t, map[string]string{
		"readme.md":       "# Hello",
		"docs/guide.txt":  "guide",
		"docs/faq.txt":    "faq",
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
	infos, err := ListFolder(a, "", "space1", "node1")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, info := range infos {
		names[info.Name] = true
	}
	if !names["readme.md"] || !names["docs"] || !names["src"] {
		t.Errorf("root listing missing entries: %v", names)
	}

	// List docs/
	infos, err = ListFolder(a, "docs", "space1", "node1")
	if err != nil {
		t.Fatal(err)
	}
	names = map[string]bool{}
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
		"sub/nested.txt": "nested",
	})

	cache := NewCache(0)
	defer cache.Close()

	a, err := cache.Get(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	// Stat file
	info, err := Stat(a, "hello.txt", "space1", "node1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "hello.txt" {
		t.Errorf("Stat name = %q, want hello.txt", info.Name)
	}

	// Stat directory
	info, err = Stat(a, "sub", "space1", "node1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "sub" {
		t.Errorf("Stat dir name = %q, want sub", info.Name)
	}

	// Download
	info, rc, err := Download(a, "hello.txt", "space1", "node1")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	buf.ReadFrom(rc)
	if buf.String() != "hello world" {
		t.Errorf("Download content = %q, want 'hello world'", buf.String())
	}
}

func TestIDConsistency(t *testing.T) {
	zipPath := createTestZip(t, map[string]string{
		"dir1/dir2/file.txt": "content",
		"dir1/other.txt":     "other",
	})

	cache := NewCache(0)
	defer cache.Close()

	a, err := cache.Get(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	spaceID := "space1"
	nodeID := "node1"

	// ListFolder dir1 → get dir2 ID
	items, err := ListFolder(a, "dir1", spaceID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	var dir2ListID string
	for _, item := range items {
		if item.Name == "dir2" {
			dir2ListID = item.Id.OpaqueId
		}
	}
	if dir2ListID == "" {
		t.Fatal("dir2 not found")
	}

	// Stat dir1/dir2 → must match
	dir2Stat, err := Stat(a, "dir1/dir2", spaceID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if dir2ListID != dir2Stat.Id.OpaqueId {
		t.Errorf("ID mismatch: ListFolder=%q, Stat=%q", dir2ListID, dir2Stat.Id.OpaqueId)
	}
}

func TestReloadRoundtrip(t *testing.T) {
	zipPath := createTestZip(t, map[string]string{
		"dir1/dir2/file.txt": "deep",
	})

	cache := NewCache(0)
	defer cache.Close()

	a, err := cache.Get(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	nodeID := "real-node-123"

	// ListFolder → get fileId → ParseArchiveID → Stat
	items, err := ListFolder(a, "dir1", "s", nodeID)
	if err != nil {
		t.Fatal(err)
	}
	var dir2FileID string
	for _, item := range items {
		if item.Name == "dir2" {
			dir2FileID = item.Id.OpaqueId
		}
	}

	parsedNode, parsedPath, ok := ParseArchiveID(dir2FileID)
	if !ok {
		t.Fatalf("ParseArchiveID(%q) failed", dir2FileID)
	}
	if parsedNode != nodeID || parsedPath != "dir1/dir2" {
		t.Errorf("parsed = (%q, %q), want (%q, %q)", parsedNode, parsedPath, nodeID, "dir1/dir2")
	}

	// No slashes in ID
	if strings.Contains(dir2FileID, "/") {
		t.Errorf("fileId contains slash: %q", dir2FileID)
	}
}

func TestParentId(t *testing.T) {
	zipPath := createTestZip(t, map[string]string{
		"a/b/file.txt": "content",
	})

	cache := NewCache(0)
	defer cache.Close()

	a, err := cache.Get(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	nodeID := "node1"
	spaceID := "space1"

	// List root → 'a' should have parent = archive root ID
	items, err := ListFolder(a, "", spaceID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name == "a" {
			if item.ParentId == nil {
				t.Error("root entry 'a' has nil ParentId")
			} else if item.ParentId.OpaqueId != nodeID+"!arc" {
				t.Errorf("root entry 'a' ParentId = %q, want %q", item.ParentId.OpaqueId, nodeID+"!arc")
			}
		}
	}

	// Stat a/b → parent should be 'a' ID
	bStat, err := Stat(a, "a/b", spaceID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if bStat.ParentId == nil {
		t.Fatal("Stat a/b has nil ParentId")
	}
	aID := makeID(spaceID, nodeID, "a")
	if bStat.ParentId.OpaqueId != aID.OpaqueId {
		t.Errorf("Stat a/b ParentId = %q, want %q", bStat.ParentId.OpaqueId, aID.OpaqueId)
	}
}

func TestIsArchiveName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"test.zip", true},
		{"test.ZIP", true},
		{"test.iso", true},
		{"test.img", true},
		{"test.squashfs", true},
		{"test.ext4", true},
		{"test.txt", false},
		{"test.pdf", false},
	}
	for _, tt := range tests {
		if got := IsArchiveName(tt.name); got != tt.want {
			t.Errorf("IsArchiveName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
