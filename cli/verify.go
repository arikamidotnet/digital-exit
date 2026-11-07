package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cmdVerify validates the pipeline output: it re-reads the dataset, checks the
// archive is present, confirms every downloaded saved-media file still exists on
// disk, and reports counts so the user can confirm before deleting anything.
func cmdVerify(l layout) error {
	ds, err := loadDataset(l.Normalized)
	if err != nil {
		return err
	}
	if ds.Version == 0 && len(ds.Contacts) == 0 && len(ds.Saved) == 0 {
		return fmt.Errorf("no dataset at %s — run 'digital-exit ingest' first", relPath(l.Root, l.Normalized))
	}

	fmt.Printf("verifying %s\n", relPath(l.Root, l.Normalized))
	if ds.AccountName != "" {
		fmt.Printf("  account:  %s\n", ds.AccountName)
	}
	fmt.Printf("  generated:%s\n", ds.GeneratedAt)

	// Archive presence.
	if dirExists(l.Archive) {
		fmt.Printf("  archive:  present (%s)\n", relPath(l.Root, l.Archive))
	} else {
		fmt.Printf("  archive:  MISSING (expected %s)\n", relPath(l.Root, l.Archive))
	}

	// Dataset integrity: re-marshal and compare byte-for-byte.
	if intact, err := datasetIntact(l.Normalized); err != nil {
		fmt.Printf("  integrity:%s (%v)\n", "CHECK FAILED", err)
	} else if intact {
		fmt.Println("  integrity: OK (data.json round-trips cleanly)")
	} else {
		fmt.Println("  integrity: data.json differs from in-memory normalization")
	}

	// Counts.
	fmt.Printf("  contacts: %d\n", len(ds.Contacts))
	var company, infl, media, friend, unclassified int
	for _, c := range ds.Contacts {
		switch c.Category {
		case CatCompany:
			company++
		case CatInfluencer:
			infl++
		case CatMedia:
			media++
		case CatFriend:
			friend++
		default:
			unclassified++
		}
	}
	fmt.Printf("    company=%d influencer=%d media=%d friend=%d unclassified=%d\n",
		company, infl, media, friend, unclassified)
	fmt.Printf("  posts:    %d\n", len(ds.Posts))

	// Saved-media integrity.
	ok, missing := 0, 0
	for _, s := range ds.Saved {
		if s.Status != "ok" {
			continue
		}
		if s.LocalPath == "" {
			missing++
			continue
		}
		full := filepath.Join(l.Root, filepath.FromSlash(s.LocalPath))
		if _, err := os.Stat(full); err == nil {
			ok++
			continue
		}
		missing++
		fmt.Printf("    MISSING media for %s (%s)\n", shortcodeOrURL(s.URL), s.LocalPath)
	}
	fmt.Printf("  saved:    %d total (ok=%d pending=%d unavailable=%d, on-disk=%d, missing=%d)\n",
		len(ds.Saved), countStatus(ds.Saved, "ok"), countStatus(ds.Saved, "pending"),
		countStatus(ds.Saved, "unavailable"), ok, missing)

	// Live background-runner progress, if one is active or exited uncleanly.
	if s, found := readFetchStatus(l); found {
		state := "idle (stale status file)"
		if s.Running {
			state = "RUNNING"
		}
		fmt.Printf("  fetch:    %s pid=%d pass=%d new=%d last=%q @ %s\n",
			state, s.PID, s.Pass, s.NewThisRun, s.LastAction, s.UpdatedAt)
	}

	// Message-media aggregation.
	mmTotal, mmDisk := 0, 0
	for _, m := range ds.Messages {
		if m.LocalPath == "" {
			continue
		}
		mmTotal++
		full := filepath.Join(l.Root, filepath.FromSlash(m.LocalPath))
		if _, err := os.Stat(full); err != nil {
			fmt.Printf("    MISSING message media: %s\n", m.LocalPath)
		} else {
			mmDisk++
		}
	}
	fmt.Printf("  messages: %d aggregated (on-disk=%d)\n", mmTotal, mmDisk)

	if missing > 0 {
		fmt.Printf("\nWARNING: %d downloaded media files are missing from disk. Re-run the downloader to retry before deleting.\n", missing)
		return nil
	}
	pendingN := countStatus(ds.Saved, "pending")
	unavailN := countStatus(ds.Saved, "unavailable")
	switch {
	case pendingN > 0:
		fmt.Printf("\nNOT READY TO DELETE: %d saved items are still pending. Keep the background runner going (./start-saved-downloads.sh) and re-run verify when pending=0.\n", pendingN)
	case unavailN > 0:
		fmt.Printf("\nVerification passed — ready for review/deletion. %d saved items are unavailable (deleted or private on Meta's side; see 03-media/unavailable.log).\n", unavailN)
	default:
		fmt.Println("\nVerification passed — archive is complete and ready for review/deletion.")
	}
	return nil
}

func countStatus(saved []SavedPost, status string) int {
	n := 0
	for _, s := range saved {
		if s.Status == status {
			n++
		}
	}
	return n
}

// datasetIntact reports whether data.json is byte-identical to a fresh marshal
// of the on-disk content, catching corruption or partial writes.
func datasetIntact(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return false, err
	}
	fresh, err := json.MarshalIndent(&ds, "", "  ")
	if err != nil {
		return false, err
	}
	return string(fresh) == string(data), nil
}

// sha256File returns the hex SHA-256 of a file (used for integrity logs).
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// summarizeSaved returns a compact status breakdown string for logs.
func summarizeSaved(saved []SavedPost) string {
	pending, ok, unavail := 0, 0, 0
	for _, s := range saved {
		switch s.Status {
		case "ok":
			ok++
		case "unavailable":
			unavail++
		default:
			pending++
		}
	}
	return strings.TrimSpace(fmt.Sprintf("pending=%d ok=%d unavailable=%d", pending, ok, unavail))
}
