package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
) 

// cmdServe starts a localhost web UI: a live dashboard for the download runner,
// contact/saved browsers, and a file manager over the export root. It binds
// 127.0.0.1 by default — nothing is exposed to the network.
func cmdServe(l layout, addr string, openBrowser bool) error {
	if _, err := os.Stat(l.Normalized); err != nil {
		return fmt.Errorf("no dataset at %s — run 'digital-exit ingest' first", relPath(l.Root, l.Normalized))
	}
	s, err := newServer(l)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/data", s.handleData)
	mux.HandleFunc("/api/contact/category", s.handleCategory)
	mux.HandleFunc("/api/fs", s.handleFS)
	mux.HandleFunc("/file", s.handleFile)
	mux.HandleFunc("/api/fetch/start", s.handleFetchStart)
	mux.HandleFunc("/api/fetch/stop", s.handleFetchStop)
	mux.HandleFunc("/api/verify", s.handleVerify)
	mux.HandleFunc("/api/ingest", s.handleIngest)
	mux.HandleFunc("/api/job", s.handleJob)
	mux.HandleFunc("/api/clean", s.handleClean)
	mux.HandleFunc("/api/cookies", s.handleCookies)
	mux.HandleFunc("/api/cookies/browser", s.handleCookiesBrowser)
	mux.HandleFunc("/api/cookies/clear", s.handleCookiesClear)
	mux.HandleFunc("/api/cookies/capture", s.handleCookiesCapture)
	mux.HandleFunc("/api/saved/retry", s.handleSavedRetry)

	url := "http://" + addr
	if openBrowser {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openInBrowser(url)
		}()
	}
	fmt.Printf("serving %s on %s (Ctrl-C to stop)\n", relPath(l.Root, l.Root), url)
	return http.ListenAndServe(addr, mux)
}

// server holds shared state for the web UI: the in-memory dataset (guarded by a
// mutex; reloaded after ingest jobs) and a single background ingest job.
type server struct {
	l   layout
	mu  sync.RWMutex
	ds  *Dataset
	job jobState
}

type jobState struct {
	mu      sync.Mutex
	kind    string // "" | "ingest"
	running bool
	log     strings.Builder
	err     error
}

func newServer(l layout) (*server, error) {
	ds, err := loadDataset(l.Normalized)
	if err != nil {
		return nil, err
	}
	return &server{l: l, ds: ds}, nil
}

func (s *server) reload() {
	if ds, err := loadDataset(s.l.Normalized); err == nil {
		s.mu.Lock()
		s.ds = ds
		s.mu.Unlock()
	}
}

// writeJSON is the shared JSON responder.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// handlePage serves the single-page UI at /.
func (s *server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderWebUI()))
}

// handleStatus reports live counts: dataset tallies + the runner's
// fetch-status.json + a few server-side facts (archive present, disk usage).
func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ds := s.ds
	s.mu.RUnlock()

	s.job.mu.Lock()
	job := map[string]any{"running": s.job.running, "kind": s.job.kind, "err": "", "log": s.job.log.String()}
	if s.job.err != nil {
		job["err"] = s.job.err.Error()
	}
	s.job.mu.Unlock()

	ok, unavail := savedStatusCounts(ds.Saved)
	st := map[string]any{
		"account":     ds.AccountName,
		"generatedAt": ds.GeneratedAt,
		"contacts":    len(ds.Contacts),
		"posts":       len(ds.Posts),
		"savedTotal":  len(ds.Saved),
		"savedOk":     ok,
		"savedUnavail": unavail,
		"savedPending": len(ds.Saved) - ok - unavail,
		"messages":    len(ds.Messages),
		"archive":     dirExists(s.l.Archive),
		"fetch":       nil,
		"job":         job,
	}
	if fs, found := readFetchStatus(s.l); found {
		st["fetch"] = fs
	}

	// Report cookie / auth state for the authenticated download flow.
	st["auth"] = cookieAuthStatus(s.l)
	writeJSON(w, 200, st)
}

// cookieAuthStatus returns presence and basic info for the two supported
// cookie sources used by yt-dlp for authenticated saved-post downloads.
func cookieAuthStatus(l layout) map[string]any {
	res := map[string]any{
		"cookiesTxt":     false,
		"cookiesTxtSize": 0,
		"cookiesBrowser": "",
	}
	if st, err := os.Stat(filepath.Join(l.Request, "cookies.txt")); err == nil && !st.IsDir() {
		res["cookiesTxt"] = true
		res["cookiesTxtSize"] = st.Size()
	}
	if b, err := os.ReadFile(filepath.Join(l.Request, "cookies-browser.txt")); err == nil {
		if name := strings.TrimSpace(string(b)); name != "" {
			res["cookiesBrowser"] = name
		}
	}
	return res
}

// handleCookies accepts a cookies.txt upload (either as multipart file "cookies"
// or as JSON {"content": "..."}) and writes it to 01-request/cookies.txt.
// This lets the user log into Instagram in their browser, export cookies.txt
// (or copy from an extension), and paste/upload directly in the web UI.
func (s *server) handleCookies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var content []byte

	// Try multipart file upload first (input type=file)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err == nil { // 8MB
			if f, _, err := r.FormFile("cookies"); err == nil {
				defer f.Close()
				content, _ = io.ReadAll(f)
			}
		}
	}

	// Fallback to JSON body { "content": "..." }
	if len(content) == 0 {
		var req struct {
			Content string `json:"content"`
		}
		if json.NewDecoder(r.Body).Decode(&req) == nil {
			content = []byte(req.Content)
		}
	}

	if len(content) == 0 {
		http.Error(w, "no cookies content provided (upload file or send {content:...})", http.StatusBadRequest)
		return
	}

	// Basic sanity: must look like a cookies.txt (contain at least one tab or instagram domain)
	text := string(content)
	if !strings.Contains(text, "instagram") && !strings.Contains(text, "\t") {
		// still accept, user might have a minimal export
	}

	if err := os.MkdirAll(s.l.Request, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(s.l.Request, "cookies.txt"), content, 0o600); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, 200, map[string]any{"ok": "cookies saved", "bytes": len(content)})
}

// handleCookiesBrowser writes 01-request/cookies-browser.txt with the chosen
// browser name (e.g. "firefox", "chrome"). yt-dlp will then use
// --cookies-from-browser <name> on the next fetch.
func (s *server) handleCookiesBrowser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Browser string `json:"browser"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Browser) == "" {
		http.Error(w, `POST {"browser": "firefox" | "chrome" | ...}`, http.StatusBadRequest)
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Browser))
	// yt-dlp supported names are typically firefox, chrome, chromium, edge, brave, opera, etc.
	if err := os.MkdirAll(s.l.Request, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(s.l.Request, "cookies-browser.txt"), []byte(name+"\n"), 0o600); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "browser set", "browser": name})
}

// handleCookiesClear removes both cookie sources so the next fetch is anonymous again.
func (s *server) handleCookiesClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = os.Remove(filepath.Join(s.l.Request, "cookies.txt"))
	_ = os.Remove(filepath.Join(s.l.Request, "cookies-browser.txt"))
	writeJSON(w, 200, map[string]string{"ok": "cookies cleared"})
}

// handleCookiesCapture uses yt-dlp to extract cookies from a local browser
// profile (after the user has logged into Instagram in that browser) and writes
// them to 01-request/cookies.txt. This is the "login in the app, it takes the cookie"
// path: user opens Instagram via the UI button, logs in in their browser, then
// clicks capture here. The server runs `yt-dlp --cookies-from-browser <name> --cookies ...`.
func (s *server) handleCookiesCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Browser string `json:"browser"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	bin := ytDlpBinary()
	if bin == "" {
		http.Error(w, "yt-dlp not found on PATH (needed to capture cookies from browser)", http.StatusPreconditionFailed)
		return
	}

	name := strings.TrimSpace(req.Browser)
	if name == "" {
		// Try a few common ones if user did not specify
		name = "firefox"
	}

	if err := os.MkdirAll(s.l.Request, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dest := filepath.Join(s.l.Request, "cookies.txt")

	// Use yt-dlp to export cookies. We pass a dummy Instagram URL and --simulate so
	// it doesn't actually download anything, just performs the cookie extraction.
	args := []string{
		"--cookies-from-browser", name,
		"--cookies", dest,
		"--simulate",
		"--no-download",
		"https://www.instagram.com/",
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// yt-dlp may return non-zero if the browser is locked or cookies not found.
		// Still check if the file was produced.
	}

	st, statErr := os.Stat(dest)
	if statErr != nil || st.Size() == 0 {
		http.Error(w, fmt.Sprintf("capture failed (browser=%s): %v\noutput: %s", name, err, string(out)), http.StatusBadRequest)
		return
	}

	writeJSON(w, 200, map[string]any{
		"ok":   "cookies captured from browser",
		"browser": name,
		"bytes": st.Size(),
	})
}

// handleSavedRetry re-queues all "unavailable" saved items (resets retries)
// and optionally launches (or signals) a runner with --retry-unavailable.
// Body: { "startRunner": true, "stopFirst": true }
func (s *server) handleSavedRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	ds := s.ds
	n := retryUnavailable(ds)
	if err := ds.save(s.l.Normalized); err != nil {
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	var req struct {
		StartRunner bool `json:"startRunner"`
		StopFirst   bool `json:"stopFirst"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.StopFirst {
		if fs, ok := readFetchStatus(s.l); ok && fs.Running && fs.PID > 0 {
			_ = syscall.Kill(fs.PID, syscall.SIGTERM)
			time.Sleep(300 * time.Millisecond)
		}
	}

	started := false
	if req.StartRunner {
		script := filepath.Join(s.l.Root, "start-saved-downloads.sh")
		if _, err := os.Stat(script); err == nil {
			cmd := exec.Command("fish", script)
			cmd.Env = append(os.Environ(), "RETRY_UNAVAILABLE=1")
			if err := cmd.Start(); err == nil {
				started = true
			}
		}
	}

	writeJSON(w, 200, map[string]any{
		"ok":           "re-queued unavailable",
		"requeued":      n,
		"runnerStarted": started,
	})
}

// handleData serves the full normalized dataset (the SPA fetches it on load).
func (s *server) handleData(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ds := s.ds
	s.mu.RUnlock()
	writeJSON(w, 200, ds)
}

// handleCategory persists a manual contact classification: POST JSON
// {"username":"x","category":"Friend"}.
func (s *server) handleCategory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Category string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	valid := false
	for _, c := range []Category{CatCompany, CatInfluencer, CatMedia, CatFriend, CatUnclassified} {
		if req.Category == string(c) {
			valid = true
		}
	}
	if !valid {
		http.Error(w, "unknown category", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for i := range s.ds.Contacts {
		if s.ds.Contacts[i].Username == req.Username {
			s.ds.Contacts[i].Category = Category(req.Category)
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "unknown contact", http.StatusNotFound)
		return
	}
	if err := s.ds.save(s.l.Normalized); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "saved"})
}

// resolveInRoot cleans a user-supplied relative path and refuses anything that
// escapes the export root. Returns the absolute path and true if safe.
func resolveInRoot(root, rel string) (string, bool) {
	rel = strings.TrimPrefix(rel, "/")
	full := filepath.Clean(filepath.Join(root, rel))
	cleanRoot := filepath.Clean(root)
	if full != cleanRoot && !strings.HasPrefix(full, cleanRoot+string(os.PathSeparator)) {
		return "", false
	}
	return full, true
}

// handleFS lists a directory under the export root: /api/fs?path=03-media
func (s *server) handleFS(w http.ResponseWriter, r *http.Request) {
	full, ok := resolveInRoot(s.l.Root, r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	type fsEntry struct {
		Name  string `json:"name"`
		Dir   bool   `json:"dir"`
		Size  int64  `json:"size"`
		IsImg bool   `json:"isImg"`
		IsVid bool   `json:"isVid"`
		IsAud bool   `json:"isAud"`
	}
	out := make([]fsEntry, 0, len(entries))
	for _, e := range entries {
		entry := fsEntry{Name: e.Name(), Dir: e.IsDir()}
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				entry.Size = info.Size()
			}
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		entry.IsImg = ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif"
		entry.IsVid = ext == ".mp4" || ext == ".webm" || ext == ".mov" || ext == ".mkv"
		entry.IsAud = ext == ".m4a" || ext == ".mp3" || ext == ".ogg" || ext == ".wav"
		out = append(out, entry)
	}
	writeJSON(w, 200, map[string]any{"path": r.URL.Query().Get("path"), "entries": out})
}

// handleFile serves any file under the export root: /file?path=03-media/saved/x.mp4
func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	full, ok := resolveInRoot(s.l.Root, r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, full)
}

// handleFetchStart launches the detached download runner (start-saved-downloads.sh
// in the export root). It returns immediately; progress comes from /api/status.
func (s *server) handleFetchStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	script := filepath.Join(s.l.Root, "start-saved-downloads.sh")
	if _, err := os.Stat(script); err != nil {
		http.Error(w, "launcher not found at "+script+" — start the runner from a terminal with 'digital-exit fetch --watch'", http.StatusNotFound)
		return
	}

	var req struct {
		RetryUnavailable bool `json:"retryUnavailable"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	env := os.Environ()
	if req.RetryUnavailable {
		env = append(env, "RETRY_UNAVAILABLE=1")
	}

	cmd := exec.Command("fish", script)
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "runner launching"})
}

// handleFetchStop asks the running download runner (per fetch-status.json) to
// finish its current item and checkpoint.
func (s *server) handleFetchStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	fs, found := readFetchStatus(s.l)
	if !found || !fs.Running {
		http.Error(w, "no runner running", http.StatusConflict)
		return
	}
	if err := syscall.Kill(fs.PID, syscall.SIGTERM); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "stop signal sent"})
}

// handleVerify runs `digital-exit verify` (this same binary) and returns its output.
func (s *server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	bin, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out, err := exec.Command(bin, "verify", "-root", s.l.Root).CombinedOutput()
	if err != nil && len(out) == 0 {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"output": string(out)})
}

// handleIngest starts a background ingest job on the given archive path
// (POST {"path": "/…/instagram-1.zip"}) — unzips/merges and re-normalizes.
func (s *server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, `POST {"path": "/path/to/export.zip"}`, http.StatusBadRequest)
		return
	}
	full, ok := resolveInRoot(s.l.Root, req.Path)
	if !ok {
		full = filepath.Clean(req.Path) // absolute path outside the root is allowed
	}
	if _, err := os.Stat(full); err != nil {
		http.Error(w, "archive not found: "+full, http.StatusNotFound)
		return
	}

	s.job.mu.Lock()
	if s.job.running {
		s.job.mu.Unlock()
		http.Error(w, "a job is already running", http.StatusConflict)
		return
	}
	s.job.running = true
	s.job.kind = "ingest " + filepath.Base(full)
	s.job.log.Reset()
	s.job.err = nil
	s.job.mu.Unlock()

	go func() {
		defer func() {
			s.job.mu.Lock()
			s.job.running = false
			s.job.mu.Unlock()
		}()
		fmt.Fprintf(&writeTo{s}, "== ingest %s ==\n", full)
		if err := cmdIngest([]string{full}, s.l); err != nil {
			fmt.Fprintf(&writeTo{s}, "error: %v\n", err)
			s.job.mu.Lock()
			s.job.err = err
			s.job.mu.Unlock()
			return
		}
		s.reload()
	}()
	writeJSON(w, 200, map[string]string{"ok": "ingest started"})
}

// writeTo adapts the server's job log into an io.Writer for cmdIngest output.
type writeTo struct{ s *server }

func (w *writeTo) Write(p []byte) (int, error) {
	w.s.job.mu.Lock()
	defer w.s.job.mu.Unlock()
	return w.s.job.log.Write(p)
}

// handleJob reports the background job state (polled by the dashboard).
func (s *server) handleJob(w http.ResponseWriter, r *http.Request) {
	s.job.mu.Lock()
	job := map[string]any{"running": s.job.running, "kind": s.job.kind, "err": "", "log": s.job.log.String()}
	if s.job.err != nil {
		job["err"] = s.job.err.Error()
	}
	s.job.mu.Unlock()
	writeJSON(w, 200, job)
}

// handleClean wipes the 02-archive staging tree (the export zips in the backup
// folder are the source of truth, so re-unzipping is always possible).
func (s *server) handleClean(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if err := os.RemoveAll(s.l.Archive); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.MkdirAll(s.l.Archive, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "staging cleaned"})
}

// openInBrowser opens a URL in the default browser (best-effort).
func openInBrowser(url string) {
	var cmd *exec.Cmd
	if _, err := os.Stat("/run/current-system/sw/bin/xdg-open"); err == nil {
		cmd = exec.Command("xdg-open", url)
	} else {
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
