package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cmdMessageGallery aggregates all DM media into one folder and emits a single
// offline HTML gallery. It is self-sufficient: if the dataset has no Messages yet,
// it re-collects them from the archived export so 'digital-exit gallery' works on
// its own after an ingest.
func cmdMessageGallery(l layout) error {
	ds, err := loadDataset(l.Normalized)
	if err != nil {
		return err
	}
	if len(ds.Messages) == 0 {
		root := filepath.Join(l.Archive, "export")
		if dirExists(root) {
			ds.Messages = collectMessageMedia(root)
		}
	}
	fmt.Printf("collecting message media: %d items\n", len(ds.Messages))
	n, err := aggregateMessageMedia(ds, l)
	if err != nil {
		return fmt.Errorf("aggregate message media: %w", err)
	}
	// Persist the renamed paths so 'verify' can confirm them and re-runs stay idempotent.
	if err := ds.save(l.Normalized); err != nil {
		return fmt.Errorf("persist message media paths: %w", err)
	}
	if err := emitMessagesGallery(ds, l); err != nil {
		return err
	}
	fmt.Printf("message gallery: %d items -> %s\n", n, relPath(l.Root, filepath.Join(l.Output, "messages-gallery.html")))
	return nil
}

// messages.go implements the "collective messages gallery": it scans every DM
// conversation in the export, gathers all message media (photos/videos/audio),
// renames each file to "<date>_<user>_<stem>.<ext>" (date from the photo's own
// creation timestamp, user from the sender), aggregates them into one folder, and
// emits a single offline HTML gallery grouped by user and sorted by date.
//
// The export stores media as raw files named by a numeric id with no useful
// filesystem mtime; the real per-file date and sender live in each message's JSON
// (message_N.json -> messages[].photos[].{creation_timestamp, uri} + sender_name).

// galleryItem is the compact, viewer-facing view of a message media file. Src is
// relative to the output directory so the gallery can point at the aggregated file.
type galleryItem struct {
	User  string `json:"user"`
	Date  string `json:"date"`
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	Src   string `json:"src"`
}

// rawMessageMedia is an intermediate record before the source path is resolved.
type rawMessageMedia struct {
	user, date, typ, title, uri, SourcePath string
}

// collectMessageMedia walks the export for every message_*.json conversation file
// and returns one MessageMedia per referenced media file, deduped by source path.
func collectMessageMedia(root string) []MessageMedia {
	var out []MessageMedia
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if !(strings.HasPrefix(name, "message") && strings.HasSuffix(name, ".json")) {
			return nil
		}
		if !strings.Contains(strings.ToLower(filepath.ToSlash(filepath.Dir(p))), "/messages/") {
			return nil
		}
		for _, rm := range extractMessageMedia(p) {
			// Some messages reference media by an absolute CDN URL (Meta did not ship the
			// local file, e.g. a photo removed from the device). Keep those as-is so the
			// gallery can link to them instead of pointing at a bogus local path.
			if strings.HasPrefix(rm.uri, "http") {
				rm.SourcePath = rm.uri
			} else {
				rm.SourcePath = filepath.Join(root, filepath.FromSlash(rm.uri))
			}
			key := filepath.Clean(rm.SourcePath)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, MessageMedia{
				User:       rm.user,
				Date:       rm.date,
				Type:       rm.typ,
				Title:      rm.title,
				SourcePath: rm.SourcePath,
			})
		}
		return nil
	})
	if out == nil {
		out = []MessageMedia{}
	}
	return out
}

// extractMessageMedia parses one conversation file into its referenced media. Each
// message may carry a "photos" or "videos" array of {uri, creation_timestamp}; any
// other array-of-{uri,...} field is treated as a generic fallback.
func extractMessageMedia(path string) []rawMessageMedia {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var conv struct {
		Title        string `json:"title"`
		Participants []struct {
			Name string `json:"name"`
		} `json:"participants"`
		Messages []map[string]any `json:"messages"`
	}
	if json.Unmarshal(data, &conv) != nil {
		return nil
	}

	title := fixMojibake(strings.TrimSpace(conv.Title))
	senderFallback := title
	for _, p := range conv.Participants {
		if n := fixMojibake(strings.TrimSpace(p.Name)); n != "" && n != senderFallback {
			senderFallback = n
		}
	}

	var out []rawMessageMedia
	for _, m := range conv.Messages {
		sender := fixMojibake(strings.TrimSpace(asString(m["sender_name"])))
		if sender == "" {
			sender = senderFallback
		}
		msgDate := messageDate(m)

		typeEntries := []struct {
			key, typ string
		}{{"photos", "photo"}, {"videos", "video"}}
		for _, te := range typeEntries {
			for _, ref := range mediaEntriesFor(m, te.key) {
				out = append(out, rawMessageMedia{
					user:  sender,
					date:  firstNonEmpty(ref.ts, msgDate),
					typ:   te.typ,
					title: title,
					uri:   ref.uri,
				})
			}
		}
		// Generic fallback: any remaining array of {uri,...} (e.g. audio_files).
		for k, v := range m {
			if k == "photos" || k == "videos" {
				continue
			}
			for _, uri := range genericMediaURIs(v) {
				out = append(out, rawMessageMedia{
					user:  sender,
					date:  msgDate,
					typ:   mediaTypeFromKey(k),
					title: title,
					uri:   uri,
				})
			}
		}
	}
	return out
}

// mediaRef is a single referenced file plus its optional per-photo timestamp.
type mediaRef struct {
	uri, ts string
}

// mediaEntriesFor pulls {uri, creation_timestamp} pairs from an array-valued field.
func mediaEntriesFor(m map[string]any, key string) []mediaRef {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []mediaRef
	for _, e := range arr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		uri := asString(em["uri"])
		if uri == "" {
			continue
		}
		out = append(out, mediaRef{uri: uri, ts: asString(em["creation_timestamp"])})
	}
	return out
}

// genericMediaURIs returns the "uri" of every object entry in an array value.
func genericMediaURIs(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range arr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if uri := asString(em["uri"]); uri != "" {
			out = append(out, uri)
		}
	}
	return out
}

// mediaTypeFromKey infers a media kind from an unexpected field name.
func mediaTypeFromKey(key string) string {
	switch strings.ToLower(key) {
	case "videos", "video":
		return "video"
	case "audio_files", "audio", "voice":
		return "audio"
	default:
		return "photo"
	}
}

// messageDate returns the YYYY-MM-DD of a message, preferring a per-photo
// creation_timestamp (unix seconds) and falling back to the message timestamp_ms.
func messageDate(m map[string]any) string {
	if sec := intNum(m["creation_timestamp"]); sec > 0 {
		return time.Unix(int64(sec), 0).UTC().Format("2006-01-02")
	}
	if ms := intNum(m["timestamp_ms"]); ms > 0 {
		return time.Unix(int64(ms/1000), 0).UTC().Format("2006-01-02")
	}
	return ""
}

// aggregateMessageMedia copies every collected message media file into
// 03-media/messages/<date>_<user>_<stem>.<ext>, recording the renamed LocalPath.
// It is non-destructive: the original export files are left untouched, so re-runs
// and the eventual deletion runbook stay safe. Returns the number of files copied.
func aggregateMessageMedia(ds *Dataset, l layout) (int, error) {
	destDir := filepath.Join(l.Media, "messages")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, err
	}
	copied := 0
	used := map[string]bool{}
	remote := 0
	for i := range ds.Messages {
		mm := &ds.Messages[i]
		if mm.SourcePath == "" {
			continue
		}
		// Idempotent: skip items already aggregated to an existing path so re-runs
		// don't pile up suffixed copies.
		if mm.LocalPath != "" {
			dest := filepath.Join(l.Root, filepath.FromSlash(mm.LocalPath))
			if _, err := os.Stat(dest); err == nil {
				copied++
				continue
			}
		}
		// Some media (voice notes, older clips) are referenced by signed CDN URL
		// rather than a bundled local file. Best-effort download: the signed links
		// may still be valid; on failure the gallery hotlinks the URL instead.
		if strings.HasPrefix(mm.SourcePath, "http://") || strings.HasPrefix(mm.SourcePath, "https://") {
			remote++
			ext := strings.ToLower(filepath.Ext(strings.SplitN(mm.SourcePath, "?", 2)[0]))
			rname := fmt.Sprintf("%s_%s_remote_%d%s", mm.Date, strings.ReplaceAll(sanitizeFilename(mm.User), " ", "_"), i, ext)
			rdest := filepath.Join(destDir, rname)
			if err := downloadFile(mm.SourcePath, rdest); err != nil {
				continue // leave LocalPath empty; gallery will use the URL
			}
			mm.LocalPath = relPath(l.Root, rdest)
			copied++
			continue
		}
		ext := strings.ToLower(filepath.Ext(mm.SourcePath))
		stem := strings.TrimSuffix(filepath.Base(mm.SourcePath), ext)
		userTok := strings.ReplaceAll(sanitizeFilename(mm.User), " ", "_")
		base := fmt.Sprintf("%s_%s_%s", mm.Date, userTok, stem)

		name := base + ext
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s_%d%s", base, n, ext)
		}
		used[name] = true

		dest := filepath.Join(destDir, name)
		if err := copyFile(mm.SourcePath, dest); err != nil {
			fmt.Printf("  message media: could not copy %s (%v)\n", relPath(l.Root, mm.SourcePath), err)
			continue
		}
		mm.LocalPath = relPath(l.Root, dest)
		copied++
	}
	if remote > 0 {
		fmt.Printf("  %d message media referenced by CDN URL (best-effort download)\n", remote)
	}
	return copied, nil
}

// emitMessagesGallery writes a single offline HTML gallery of all message media to
// 05-output/messages-gallery.html, grouped by user and sorted by date.
func emitMessagesGallery(ds *Dataset, l layout) error {
	items := galleryItems(ds, l)
	if len(items) == 0 {
		fmt.Println("no message media to gallery")
	}
	// Always write the artifact (even when empty) so downstream steps like 'verify'
	// and the runtime validator have a file to read — consistent with emitViewer.
	if err := os.MkdirAll(l.Output, 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("encode message gallery: %w", err)
	}
	title := "Message gallery — " + ds.AccountName
	html := renderMessagesGallery(title, base64.StdEncoding.EncodeToString(payload), relPath(l.Output, filepath.Join(l.Media, "messages")))
	out := filepath.Join(l.Output, "messages-gallery.html")
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		return fmt.Errorf("write message gallery: %w", err)
	}
	return nil
}

// galleryItems builds the viewer-facing list, computing each item's src relative to
// the output directory so the standalone HTML can reach the aggregated files.
func galleryItems(ds *Dataset, l layout) []galleryItem {
	items := make([]galleryItem, 0, len(ds.Messages))
	for _, mm := range ds.Messages {
		abs := mm.LocalPath
		if abs == "" {
			abs = mm.SourcePath
		}
		var src string
		if strings.HasPrefix(abs, "http") {
			src = abs // external CDN reference — link directly (works online)
		} else {
			src = relPath(l.Output, filepath.FromSlash(abs))
		}
		items = append(items, galleryItem{
			User:  mm.User,
			Date:  mm.Date,
			Type:  mm.Type,
			Title: mm.Title,
			Src:   src,
		})
	}
	return items
}

// downloadFile fetches url into dst (best effort, single attempt).
func downloadFile(url, dst string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	if err := ensureParentFile(dst); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := ensureParentFile(dst); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// firstNonEmpty returns the first non-empty of its arguments.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// renderMessagesGallery builds the gallery HTML. itemsB64 is base64-encoded JSON and
// mediaBase is the path from the output dir to the aggregated media folder.
func renderMessagesGallery(title, itemsB64, mediaBase string) string {
	esc := escapeJSString(title)
	html := `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__TITLE__</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; font: 14px/1.5 -apple-system, Segoe UI, Roboto, Helvetica, Arial, sans-serif;
         background: #0d1117; color: #c9d1d9; }
  header { padding: 16px 20px; border-bottom: 1px solid #21262d; display: flex;
           align-items: center; gap: 12px; flex-wrap: wrap; position: sticky; top: 0;
           background: #0d1117; z-index: 5; }
  h1 { font-size: 16px; margin: 0; color: #f0f6fc; flex: 1; min-width: 200px; }
  input[type=search] { background: #0d1117; color: #c9d1d9; border: 1px solid #30363d;
           padding: 7px 10px; border-radius: 6px; min-width: 220px; }
  main { padding: 16px 20px; max-width: 1400px; margin: 0 auto; }
  .stat { color: #8b949e; font-size: 13px; margin-bottom: 12px; }
  h2 { font-size: 14px; color: #f0f6fc; border-bottom: 1px solid #21262d; padding-bottom: 6px;
       margin-top: 26px; }
  .count { color: #8b949e; font-weight: normal; }
  .grid { display: grid; gap: 8px; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); }
  figure { margin: 0; background: #161b22; border: 1px solid #21262d; border-radius: 8px;
           overflow: hidden; }
  figure img, figure video { width: 100%; aspect-ratio: 1/1; object-fit: cover; display: block; background: #010409; }
  figure audio { width: 100%; }
  figcaption { font-size: 11px; color: #8b949e; padding: 5px 7px; white-space: nowrap;
           overflow: hidden; text-overflow: ellipsis; }
  .hidden { display: none !important; }
  footer { color: #484f58; font-size: 12px; text-align: center; padding: 24px; }
</style>
</head>
<body>
<header>
  <h1 id="title">__TITLE__</h1>
  <input type="search" id="search" placeholder="Filter by user, date, or filename…">
</header>
<main>
  <div class="stat" id="stat"></div>
  <div id="gallery"></div>
</main>
<footer>Digital-Exit message gallery — fully offline. Media referenced from __MEDIA_BASE__.</footer>
<script>
const ITEMS = JSON.parse(atob("__DATA__"));
let query = "";

function escapeHtml(s) { return String(s==null?"":s).replace(/[&<>]/g, c => ({ "&":"&amp;","<":"&lt;",">":"&gt;" }[c])); }
function escapeAttr(s) { return escapeHtml(s).replace(/"/g, "&quot;"); }

function mediaEl(it) {
  if (it.type === "video") {
    return '<video src="' + escapeAttr(it.src) + '" controls preload="metadata"></video>';
  }
  if (it.type === "audio") {
    return '<audio src="' + escapeAttr(it.src) + '" controls preload="metadata"></audio>';
  }
  return '<img src="' + escapeAttr(it.src) + '" alt="" loading="lazy">';
}

function render() {
  // Group by user, sort each group by date then filename.
  const groups = {};
  ITEMS.forEach(it => {
    const u = it.user || "(unknown)";
    (groups[u] = groups[u] || []).push(it);
  });
  const users = Object.keys(groups).sort((a, b) => a.localeCompare(b));
  users.forEach(u => groups[u].sort((a, b) => {
    if (a.date !== b.date) return a.date < b.date ? -1 : 1;
    return a.src < b.src ? -1 : (a.src > b.src ? 1 : 0);
  }));

  const q = query.toLowerCase();
  let shown = 0, total = 0;
  const out = [];
  users.forEach(u => {
    const items = groups[u].filter(it => {
      total++;
      const hay = (u + " " + it.date + " " + (it.title || "") + " " + it.src).toLowerCase();
      if (!q || hay.indexOf(q) !== -1) { shown++; return true; }
      return false;
    });
    if (items.length === 0 && q) return;
    out.push('<h2>' + escapeHtml(u) + ' <span class="count">(' + items.length + ')</span></h2>');
    if (items.length === 0) return;
    out.push('<div class="grid">');
    items.forEach(it => {
      const cap = [it.date, basename(it.src)].filter(Boolean).join(" · ");
      out.push('<figure data-q="' + escapeAttr((u + " " + it.date + " " + it.src).toLowerCase()) + '">'
        + mediaEl(it)
        + '<figcaption>' + escapeHtml(cap) + '</figcaption></figure>');
    });
    out.push('</div>');
  });

  const gallery = document.getElementById("gallery");
  gallery.innerHTML = out.join("");
  document.getElementById("stat").textContent =
    shown + " of " + total + " media shown · grouped by user, sorted by date";
}

function basename(p) { const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\")); return i >= 0 ? p.slice(i + 1) : p; }

document.getElementById("search").addEventListener("input", e => { query = e.target.value; render(); });
render();
</script>
</body>
</html>
`
	html = strings.ReplaceAll(html, "__DATA__", itemsB64)
	html = strings.ReplaceAll(html, "__TITLE__", esc)
	html = strings.ReplaceAll(html, "__TITLE_ESC__", esc)
	html = strings.ReplaceAll(html, "__MEDIA_BASE__", mediaBase)
	return html
}
