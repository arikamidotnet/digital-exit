package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const maxRetries = 3

// fetchState carries progress bookkeeping across one or more download passes.
// A nil *fetchState is valid — fetchSavedPosts then runs a plain one-off pass
// without status-file tracking.
type fetchState struct {
	l          layout
	started    time.Time
	lastSave   time.Time
	pass       int
	actions    int // actions since last checkpoint
	totalNew   int // downloads completed across all passes
	lastAction string
	track      bool         // write 04-processed/fetch-status.json at checkpoints
	stop       *atomic.Bool // graceful stop flag (nil = unstoppable)
	regen      func()       // optional throttled viewer regeneration
}

// checkpoint persists the dataset (and status file) once enough work has
// happened or enough time has passed. force=true saves unconditionally.
func (st *fetchState) checkpoint(ds *Dataset, force bool) {
	now := time.Now()
	if !force && st.actions < 10 && now.Sub(st.lastSave) < time.Minute {
		return
	}
	if err := ds.save(st.l.Normalized); err != nil {
		fmt.Printf("  (warning: could not checkpoint dataset: %v)\n", err)
		return
	}
	st.actions = 0
	st.lastSave = now
	if st.track {
		writeFetchStatus(st.l, ds, st)
	}
	if st.regen != nil {
		st.regen()
	}
}

// stopped reports whether a graceful stop was requested.
func (st *fetchState) stopped() bool { return st.stop != nil && st.stop.Load() }

// fetchStatus is the live progress snapshot written to
// 04-processed/fetch-status.json so verify and the viewer can show download
// progress while the background runner works.
type fetchStatus struct {
	Running     bool   `json:"running"`
	PID         int    `json:"pid"`
	StartedAt   string `json:"startedAt"`
	UpdatedAt   string `json:"updatedAt"`
	Pass        int    `json:"pass"`
	OK          int    `json:"ok"`
	Pending     int    `json:"pending"`
	Unavailable int    `json:"unavailable"`
	NewThisRun  int    `json:"newThisRun"`
	LastAction  string `json:"lastAction"`
}

func fetchStatusPath(l layout) string { return filepath.Join(l.Processed, "fetch-status.json") }

func writeFetchStatus(l layout, ds *Dataset, st *fetchState) {
	ok, unavail := savedStatusCounts(ds.Saved)
	s := fetchStatus{
		Running:     true,
		PID:         os.Getpid(),
		StartedAt:   st.started.UTC().Format(time.RFC3339),
		UpdatedAt:   nowRFC3339(),
		Pass:        st.pass,
		OK:          ok,
		Pending:     len(ds.Saved) - ok - unavail,
		Unavailable: unavail,
		NewThisRun:  st.totalNew,
		LastAction:  st.lastAction,
	}
	b, err := json.MarshalIndent(&s, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(fetchStatusPath(l), b, 0o644); err != nil {
		fmt.Printf("  (warning: could not write fetch status: %v)\n", err)
	}
}

// pidAlive reports whether a process with the given pid exists.
func pidAlive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

// readFetchStatus loads the live progress snapshot, if one exists. A snapshot
// left behind by a runner that died without cleanup (kill -9, power loss)
// is corrected to idle by checking the recorded pid.
func readFetchStatus(l layout) (*fetchStatus, bool) {
	b, err := os.ReadFile(fetchStatusPath(l))
	if err != nil {
		return nil, false
	}
	var s fetchStatus
	if json.Unmarshal(b, &s) != nil {
		return nil, false
	}
	if s.Running && !pidAlive(s.PID) {
		s.Running = false
	}
	return &s, true
}

// clearFetchStatus removes the progress snapshot after a clean exit.
func clearFetchStatus(l layout) { _ = os.Remove(fetchStatusPath(l)) }

// fetchSavedPosts downloads every pending saved post with yt-dlp, retrying
// failures up to maxRetries before marking a post unavailable. It mutates ds.Saved.
// If maxItems > 0, at most that many *new* downloads (network or first-time)
// will be attempted in this call; already-ok and on-disk items are skipped
// without counting against the limit. This enables safe batching:
//   digital-exit run --saved-limit 300
// The dataset is saved periodically so that an interrupted run (power loss,
// disconnect, Ctrl-C) preserves progress made so far.
//
// Important: if yt-dlp is not on PATH, we still adopt any files that are
// already on disk (resuming prior partial runs) and leave the rest pending
// instead of marking them unavailable. Only when we actually need to
// perform a network download do we require the binary.
func fetchSavedPosts(ds *Dataset, l layout, maxItems int, st *fetchState) error {
	if len(ds.Saved) == 0 {
		fmt.Println("no saved posts to fetch")
		return nil
	}
	if err := os.MkdirAll(l.SavedMedia, 0o755); err != nil {
		return err
	}
	if st == nil {
		st = &fetchState{l: l, started: time.Now(), lastSave: time.Now()}
	}

	ok, unavail := 0, 0
	fetchedThisRun := 0

	// Lazily resolve the binary only when we are about to download.
	var bin string
	binChecked := false
	var authArgs []string

	// Reconcile every existing file before any network work or batch cutoff.
	for i := range ds.Saved {
		sp := &ds.Saved[i]
		if existing := findExistingSavedMedia(l.SavedMedia, sp.URL); existing != "" {
			sp.Status = "ok"
			sp.LocalPath = relPath(l.Root, existing)
		} else if sp.Status == "ok" {
			sp.Status = "pending"
			sp.LocalPath = ""
		}
	}
	st.checkpoint(ds, true)

	for i := 0; i < len(ds.Saved); i++ {
		sp := &ds.Saved[i]

		if st.stopped() {
			fmt.Println("\n== stop requested — saving progress, exiting after this item ==")
			break
		}

		switch sp.Status {
		case "ok":
			ok++
			continue
		case "unavailable":
			unavail++
			continue
		}

		// Resumability: if a file for this shortcode already exists on disk
		// (e.g. previous run was killed after writing the file but before saving
		// the dataset), adopt it instead of re-downloading.
		if existing := findExistingSavedMedia(l.SavedMedia, sp.URL); existing != "" {
			sp.Status = "ok"
			sp.LocalPath = relPath(l.Root, existing)
			ok++
			st.lastAction = "adopted " + shortcodeOrURL(sp.URL)
			fmt.Printf("  resumed %s (already on disk)\n", shortcodeOrURL(sp.URL))
			st.actions++
			st.checkpoint(ds, false)
			continue
		}

		// Respect batch limit: only count actual new download attempts.
		if maxItems > 0 && fetchedThisRun >= maxItems {
			// Stop processing more; leave the rest pending.
			break
		}

		// Need to download a new item. Ensure we have yt-dlp.
		if !binChecked {
			bin = ytDlpBinary()
			authArgs = ytDlpAuthArgs(l)
			binChecked = true
			if bin == "" {
				fmt.Println("yt-dlp not found on PATH; cannot fetch more saved media.")
				fmt.Println("(Install it or enter the nix shell that provides it. Existing on-disk files are kept; remaining items stay pending for the next run.)")
				break
			}
		}

		fetchedThisRun++
		st.totalNew++
		path, err := ytDlpFetch(bin, authArgs, sp.URL, l.SavedMedia)
		if err != nil || path == "" {
			sp.Retries++
			if sp.Retries >= maxRetries {
				sp.Status = "unavailable"
				unavail++
				appendLog(l.Unavailable, fmt.Sprintf("%s\tauthor=%s\tretries=%d\terr=%v", sp.URL, sp.Author, sp.Retries, err))
				fmt.Printf("  unavailable %s (%d retries)\n", shortcodeOrURL(sp.URL), sp.Retries)
				st.lastAction = "unavailable " + shortcodeOrURL(sp.URL)
			} else {
				fmt.Printf("  retry %d/%d for %s\n", sp.Retries, maxRetries, shortcodeOrURL(sp.URL))
				// Transient failure: stays pending for a later pass.
				st.lastAction = "retry " + shortcodeOrURL(sp.URL)
			}
		} else {
			sp.Status = "ok"
			sp.LocalPath = relPath(l.Root, path)
			ok++
			fmt.Printf("  saved %s -> %s\n", shortcodeOrURL(sp.URL), relPath(l.SavedMedia, path))
			st.lastAction = "downloaded " + shortcodeOrURL(sp.URL)
		}
		st.actions++
		st.checkpoint(ds, false)
	}

	// Final checkpoint so even a small batch, a stop, or the end of a run is durable.
	st.checkpoint(ds, true)

	fmt.Printf("saved media: %d ok, %d unavailable (this pass fetched %d new)\n", ok, unavail, fetchedThisRun)
	if maxItems > 0 {
		fmt.Printf("  batch limit was %d; more may remain pending\n", maxItems)
	}
	return nil
}

// ytDlpBinary returns the yt-dlp executable name if found on PATH.
func ytDlpBinary() string {
	if bin, err := exec.LookPath("yt-dlp"); err == nil {
		return bin
	}
	if _, err := os.Stat("yt-dlp.exe"); err == nil {
		return "yt-dlp.exe"
	}
	return ""
}

// ytDlpAuthArgs returns optional authentication arguments for yt-dlp.
// Two cookie sources are supported in 01-request/ (checked in this order):
//   - cookies.txt: a cookies.txt export from a browser logged into Instagram
//   - cookies-browser.txt: a file containing a browser name (e.g. "firefox"),
//     used with yt-dlp's --cookies-from-browser (close the browser first)
// With cookies, yt-dlp can download posts the logged-in account can see,
// including private accounts it follows. Without them only public posts work.
func ytDlpAuthArgs(l layout) []string {
	if b, err := os.ReadFile(filepath.Join(l.Request, "cookies.txt")); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return []string{"--cookies", filepath.Join(l.Request, "cookies.txt")}
	}
	if b, err := os.ReadFile(filepath.Join(l.Request, "cookies-browser.txt")); err == nil {
		if name := strings.TrimSpace(string(b)); name != "" {
			return []string{"--cookies-from-browser", name}
		}
	}
	return nil
}

// retryUnavailable re-queues every unavailable saved post so a later pass can
// try again — used after adding cookies, since items that failed anonymously
// may succeed authenticated. Retry counters are reset with the statuses.
func retryUnavailable(ds *Dataset) int {
	n := 0
	for i := range ds.Saved {
		if ds.Saved[i].Status == "unavailable" {
			ds.Saved[i].Status = "pending"
			ds.Saved[i].Retries = 0
			n++
		}
	}
	return n
}

// ytDlpFetch downloads a single URL into dir and returns the path of the file
// that was written (resolving the %(ext)s placeholder).
func ytDlpFetch(bin string, authArgs []string, target, dir string) (string, error) {
	sc := shortcodeFromURL(target)
	if sc == "" {
		sc = sanitizeFilename(target)
	}
	os.MkdirAll(dir, 0o755)
	outPath := filepath.Join(dir, sc+".%(ext)s")
	args := append([]string{
		"-f", "bestvideo[ext=mp4]+bestaudio/best,[ext=mp4]",
		"--no-playlist", "--retries", "3", "--no-overwrites",
	}, authArgs...)
	args = append(args, "-o", outPath, target)
	cmd := exec.Command(bin, args...)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	path := findExistingSavedMedia(dir, target)
	if path == "" {
		return "", fmt.Errorf("no output produced for %s", target)
	}
	return path, nil
}

// findExistingSavedMedia looks for any already-downloaded file for the given
// saved post URL inside dir. It matches on the Instagram shortcode (or a
// sanitized fallback) so that interrupted downloads that left a file on disk
// can be adopted without re-fetching.
func findExistingSavedMedia(dir, url string) string {
	sc := shortcodeFromURL(url)
	if sc == "" {
		sc = sanitizeFilename(url)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, sc+".*"))
	for _, path := range matches {
		name := filepath.Base(path)
		ext := strings.ToLower(filepath.Ext(path))
		if strings.Contains(name, ".fdash-") || strings.Contains(name, ".fvideo") {
			continue
		}
		switch ext {
		case ".mp4", ".webm", ".mov", ".mkv", ".jpg", ".jpeg", ".png", ".webp", ".m4a", ".mp3", ".ogg":
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				return path
			}
		}
	}
	return ""
}

// shortcodeOrURL returns the post shortcode or a short URL fragment for logging.
func shortcodeOrURL(u string) string {
	if sc := shortcodeFromURL(u); sc != "" {
		return sc
	}
	return u
}

// enrichFollowers optionally fills in follower counts and bios from the Instagram
// Graph API using a token stored at 01-request/auth.json. It is best-effort: any
// failure (missing token, rate limit) leaves the contact unchanged.
func enrichFollowers(contacts []Contact, l layout) {
	token := readToken(l.Request)
	if token == "" {
		fmt.Println("no auth token at 01-request/auth.json; skipping API enrichment")
		return
	}
	enriched, skipped := 0, 0
	for i := range contacts {
		c := &contacts[i]
		if c.FollowerCount > 0 && strings.TrimSpace(c.Bio) != "" {
			skipped++
			continue
		}
		if enrichOne(c, token, l.Unavailable) {
			enriched++
		}
	}
	fmt.Printf("enrichment: %d updated, %d already complete\n", enriched, skipped)
}

// readToken loads the Graph API access token from auth.json if present.
func readToken(reqDir string) string {
	data, err := os.ReadFile(filepath.Join(reqDir, "auth.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		AccessToken string `json:"access_token"`
		Token       string `json:"token"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	if cfg.AccessToken != "" {
		return cfg.AccessToken
	}
	return cfg.Token
}

type igSearchEntry struct {
	ID             string `json:"id"`
	FollowersCount int    `json:"followers_count"`
	Biography      string `json:"biography"`
}

type igSearchResp struct {
	Data  []igSearchEntry `json:"data"`
	Error struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// enrichOne resolves a username via the Graph search endpoint and fills in the
// follower count and biography. Returns true on success.
func enrichOne(c *Contact, token, logPath string) bool {
	if c.Username == "" {
		return false
	}
	q := url.Values{}
	q.Set("access_token", token)
	q.Set("q", c.Username)
	q.Set("type", "user")
	q.Set("fields", "id,followers_count,biography")
	q.Set("limit", "1")
	endpoint := "https://graph.instagram.com/search?" + q.Encode()

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := http.Get(endpoint)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(2*(attempt+1)) * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var r igSearchResp
		if json.Unmarshal(body, &r) == nil && len(r.Data) > 0 {
			d := r.Data[0]
			if d.FollowersCount > 0 {
				c.FollowerCount = d.FollowersCount
			}
			if strings.TrimSpace(d.Biography) != "" {
				c.Bio = fixMojibake(strings.TrimSpace(d.Biography))
				if c.DisplayName == "" || c.DisplayName == c.Username {
					c.DisplayName = c.Username
				}
			}
			return true
		}
		// Rate limited or transient — back off and retry.
		lastErr = fmt.Errorf("graph api: %s", r.Error.Message)
		time.Sleep(time.Duration(2*(attempt+1)) * time.Second)
	}
	if lastErr != nil && logPath != "" {
		appendLog(logPath, fmt.Sprintf("%s\tenrich_failed\t%v", c.Username, lastErr))
	}
	return false
}
