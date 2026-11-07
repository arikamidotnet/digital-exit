package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRootBeforeLimit(t *testing.T) {
	for _, args := range [][]string{
		{"-root", "/export", "--saved-limit", "5"},
		{"-root=/export", "--saved-limit", "5"},
	} {
		root, rest := parseRoot(args)
		if root != "/export" || parseIntFlag(rest, "--saved-limit", "-saved-limit") != 5 {
			t.Fatalf("root=%q rest=%v", root, rest)
		}
	}
}

func TestExistingSavedMediaRejectsIncompleteFiles(t *testing.T) {
	dir := t.TempDir()
	u := "https://www.instagram.com/p/abc/"
	for _, name := range []string{"abc.mp4.part", "abc.fdash-123.mp4", "abc.info.json", "abc.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := findExistingSavedMedia(dir, u); got != "" {
		t.Fatalf("adopted incomplete file: %s", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := findExistingSavedMedia(dir, u); got != filepath.Join(dir, "abc.mp4") {
		t.Fatalf("completed file not found: %s", got)
	}
}

func TestReconcileWithoutDownloader(t *testing.T) {
	l := newLayout(t.TempDir())
	if err := ensureDirs(l); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	os.WriteFile(filepath.Join(l.SavedMedia, "existing.mp4"), []byte("media"), 0600)
	ds := &Dataset{Saved: []SavedPost{
		{URL: "https://www.instagram.com/p/missing/", Status: "pending"},
		{URL: "https://www.instagram.com/p/existing/", Status: "pending"},
	}}
	if err := fetchSavedPosts(ds, l, 1, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := loadDataset(l.Normalized)
	if err != nil || saved.Saved[1].Status != "ok" || saved.Saved[0].Status != "pending" {
		t.Fatalf("reconciliation failed: %v %+v", err, saved)
	}
}

func TestFetchPassesAreIdempotent(t *testing.T) {
	l := newLayout(t.TempDir())
	if err := ensureDirs(l); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	os.WriteFile(filepath.Join(l.SavedMedia, "existing.mp4"), []byte("media"), 0o600)
	ds := &Dataset{Saved: []SavedPost{
		{URL: "https://www.instagram.com/p/missing/", Status: "pending"},
		{URL: "https://www.instagram.com/p/existing/", Status: "pending"},
	}}
	for pass := 0; pass < 2; pass++ {
		if err := fetchSavedPosts(ds, l, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	if ds.Saved[1].Status != "ok" || ds.Saved[0].Status != "pending" || ds.Saved[0].Retries != 0 {
		t.Fatalf("repeated passes changed state: %+v", ds.Saved)
	}
}

func TestNextWatchPass(t *testing.T) {
	if _, cont := nextWatchPass(0, 10, 10, 0); cont {
		t.Fatal("drained queue should stop the watch loop")
	}
	if s, cont := nextWatchPass(5, 10, 15, 2); !cont || s != 0 {
		t.Fatalf("progress should reset the stall counter: got %d %v", s, cont)
	}
	if s, cont := nextWatchPass(5, 10, 10, 0); !cont || s != 1 {
		t.Fatalf("first stalled pass should continue: got %d %v", s, cont)
	}
	if s, cont := nextWatchPass(5, 10, 10, 2); cont || s != 3 {
		t.Fatalf("third stalled pass should stop: got %d %v", s, cont)
	}
}
