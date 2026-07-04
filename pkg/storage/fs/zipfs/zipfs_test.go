package zipfs

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
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
	infos, err := ListFolder(a, "", "test-space")
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
	infos, err = ListFolder(a, "docs", "test-space")
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
	info, err := Stat(a, "hello.txt", "test-space")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "hello.txt" {
		t.Errorf("Stat name = %q, want hello.txt", info.Name)
	}

	// Stat directory
	info, err = Stat(a, "sub", "test-space")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "sub" {
		t.Errorf("Stat dir name = %q, want sub", info.Name)
	}

	// Download
	info, rc, err := Download(a, "hello.txt", "test-space")
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
	_, err = Stat(a, "nonexistent", "test-space")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}
