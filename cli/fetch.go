package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

// cmdFetch runs the saved-post media fetch step. It is resumable: on-disk files
// are adopted without re-download, already-ok entries are skipped, and the
// dataset is checkpointed during the run so a disconnect loses at most a few
// items. --saved-limit caps new downloads per pass; without it the whole
// pending queue is processed in order.
//
// With --watch the command becomes a long-lived background runner: it loops
// download passes (with retry delays) until the queue drains or stalls, holds a
// single-instance lock, and keeps 04-processed/fetch-status.json updated so
// verify and the viewer show live progress. Launch it detached via
// start-saved-downloads.sh so it survives terminal disconnects.
func cmdFetch(l layout, savedLimit int, watch bool, retryUnavail bool) error {
	if err := ensureDirs(l); err != nil {
		return err
	}
	ds, err := loadDataset(l.Normalized)
	if err != nil {
		return err
	}
	if ds.Version > datasetVersion {
		fmt.Printf("warning: dataset version %d is newer than tool's %d; some fields may be missing\n", ds.Version, datasetVersion)
	}
	if retryUnavail {
		if n := retryUnavailable(ds); n > 0 {
			fmt.Printf("re-queued %d previously-unavailable items for retry (e.g. after adding cookies)\n", n)
		} else {
			fmt.Println("no unavailable items to re-queue")
		}
	}

	stop := installStopHandler()

	if watch {
		lock, err := acquireFetchLock(l)
		if err != nil {
			return err
		}
		defer lock.Close()
		return runFetchWatch(ds, l, savedLimit, stop)
	}

	fmt.Println("== fetching saved-post media (standalone) ==")
	if savedLimit > 0 {
		fmt.Printf("  batch limit: %d new downloads max\n", savedLimit)
	} else {
		fmt.Println("  batch limit: none (fetch the whole pending queue)")
	}

	st := &fetchState{l: l, started: time.Now(), lastSave: time.Now(), stop: stop, track: true}
	if err := fetchSavedPosts(ds, l, savedLimit, st); err != nil {
		return fmt.Errorf("fetch saved media: %w", err)
	}

	// fetchSavedPosts already checkpoints; save once more for certainty.
	if err := ds.save(l.Normalized); err != nil {
		return err
	}
	clearFetchStatus(l)

	ok, unavail := savedStatusCounts(ds.Saved)
	fmt.Printf("\nSaved media done. Total: %d (ok=%d unavailable=%d pending=%d)\n",
		len(ds.Saved), ok, unavail, len(ds.Saved)-ok-unavail)
	fmt.Printf("Media dir: %s\n", l.SavedMedia)
	return nil
}

// acquireFetchLock takes an exclusive non-blocking flock on
// 04-processed/fetch.lock so exactly one downloader writes the dataset at a
// time. The lock is held until the process exits.
func acquireFetchLock(l layout) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(l.Processed, "fetch.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another download runner is already active for this root")
	}
	return f, nil
}

// installStopHandler turns the first SIGINT/SIGTERM into a graceful stop: the
// current download finishes, progress is checkpointed, the runner exits cleanly.
// A second signal force-exits immediately.
func installStopHandler() *atomic.Bool {
	stop := new(atomic.Bool)
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		fmt.Println("\n== stop requested — finishing current download, then saving ==")
		stop.Store(true)
		<-ch
		fmt.Println("== second signal — exiting now ==")
		os.Exit(130)
	}()
	return stop
}

// runFetchWatch loops download passes until the queue drains or the runner
// stalls: reconcile → download everything pending → checkpoint → pause →
// retry. Progress resets the stall counter; three consecutive passes with no
// new downloads mean the rest are not coming back right now, so it stops and
// leaves them pending (re-run later or check 03-media/unavailable.log).
func runFetchWatch(ds *Dataset, l layout, savedLimit int, stop *atomic.Bool) error {
	fmt.Println("== fetch watch: draining the saved queue, one pass at a time ==")
	if savedLimit > 0 {
		fmt.Printf("  per-pass limit: %d new downloads\n", savedLimit)
	}
	st := &fetchState{l: l, started: time.Now(), lastSave: time.Now(), stop: stop, track: true}

	// Keep the offline viewer's dashboard roughly current (throttled to 5 min).
	lastRegen := time.Now()
	st.regen = func() {
		if time.Since(lastRegen) < 5*time.Minute {
			return
		}
		lastRegen = time.Now()
		if err := emitViewer(ds, l); err != nil {
			fmt.Printf("  (warning: viewer regen failed: %v)\n", err)
		}
	}

	stalled := 0
	for {
		st.pass++
		okBefore, _ := savedStatusCounts(ds.Saved)
		if err := fetchSavedPosts(ds, l, savedLimit, st); err != nil {
			return err
		}
		okAfter, unavail := savedStatusCounts(ds.Saved)
		pending := len(ds.Saved) - okAfter - unavail

		var cont bool
		stalled, cont = nextWatchPass(pending, okBefore, okAfter, stalled)
		if !cont {
			fmt.Printf("watch done after pass %d (pending=%d, unavailable=%d)\n", st.pass, pending, unavail)
			break
		}
		if stop.Load() {
			fmt.Println("watch stopped by signal")
			break
		}

		fmt.Printf("pass %d complete: %d downloaded so far, %d still pending; next retry pass in 10m (Ctrl-C stops cleanly)\n",
			st.pass, st.totalNew, pending)
		// Sleep in short slices so a signal stops the runner promptly.
		for i := 0; i < 60 && !stop.Load(); i++ {
			time.Sleep(10 * time.Second)
		}
	}

	if err := ds.save(l.Normalized); err != nil {
		return err
	}
	clearFetchStatus(l)

	ok, unavail := savedStatusCounts(ds.Saved)
	pending := len(ds.Saved) - ok - unavail
	fmt.Printf("\nwatch finished: ok=%d pending=%d unavailable=%d (downloaded %d new this run)\n", ok, pending, unavail, st.totalNew)
	if pending > 0 {
		fmt.Println("Remaining items are pending — re-run 'fetch --watch' another day; they may only be temporarily unreachable.")
	}
	return nil
}

// nextWatchPass decides whether another retry pass is worth running: stop when
// the queue is drained, reset the stall counter on progress, and stop after
// three consecutive stalled passes. Returns the updated stall count and
// whether to continue.
func nextWatchPass(pending, okBefore, okAfter, stalled int) (int, bool) {
	if pending == 0 {
		return 0, false
	}
	if okAfter > okBefore {
		return 0, true
	}
	return stalled + 1, stalled+1 < 3
}
