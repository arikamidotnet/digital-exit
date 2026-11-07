package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// emitViewer writes an offline, self-contained HTML viewer to 05-output/index.html.
// The dataset is embedded as base64 and decoded at runtime so no server is needed.
func emitViewer(ds *Dataset, l layout) error {
	if err := os.MkdirAll(l.Output, 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(ds)
	if err != nil {
		return fmt.Errorf("encode dataset for viewer: %w", err)
	}
	title := "digital-exit — " + ds.AccountName
	html := renderViewer(title, base64.StdEncoding.EncodeToString(payload))
	out := filepath.Join(l.Output, "index.html")
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		return fmt.Errorf("write viewer: %w", err)
	}
	return nil
}

// cmdView opens the generated viewer in the default browser.
func cmdView(l layout) error {
	out := filepath.Join(l.Output, "index.html")
	if _, err := os.Stat(out); err != nil {
		return fmt.Errorf("viewer not found at %s — run 'digital-exit run' first", relPath(l.Root, out))
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", out)
	case "windows":
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", out)
	default:
		cmd = exec.Command("xdg-open", out)
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("could not auto-open browser (%v); open this file manually:\n%s\n", err, relPath(l.Root, out))
		return nil
	}
	fmt.Println("opened:", relPath(l.Root, out))
	return nil
}

// escapeJSString makes title safe to embed inside a JS double-quoted string.
func escapeJSString(s string) string {
	repl := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "<", "&lt;", ">", "&gt;")
	return repl.Replace(s)
}

// renderViewer builds the HTML document. dataB64 is base64-encoded JSON and title
// is the page heading. Placeholders are substituted so the raw string needs no
// embedded Go concatenation (which would require matching backticks).
func renderViewer(title, dataB64 string) string {
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
  .tabs { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
  .tabs button, .filters button { background: #161b22; color: #c9d1d9; border: 1px solid #30363d;
           padding: 6px 12px; border-radius: 6px; cursor: pointer; font-size: 13px; }
  .tabs button.active, .filters button.active { background: #238636; border-color: #238636; color: #fff; }
  .tabs a.tablink { background: #161b22; color: #79c0ff; border: 1px solid #30363d;
           padding: 6px 12px; border-radius: 6px; font-size: 13px; text-decoration: none; }
  input[type=search] { background: #0d1117; color: #c9d1d9; border: 1px solid #30363d;
           padding: 7px 10px; border-radius: 6px; min-width: 220px; }
  main { padding: 16px 20px; max-width: 1100px; margin: 0 auto; }
  .stat { color: #8b949e; font-size: 13px; margin-bottom: 12px; }
  .card { background: #161b22; border: 1px solid #21262d; border-radius: 8px; padding: 12px 14px;
          margin-bottom: 10px; display: flex; gap: 12px; align-items: flex-start; }
  .avatar { width: 40px; height: 40px; border-radius: 50%; background: #30363d; flex: none;
            display: grid; place-items: center; font-weight: 600; color: #f0f6fc; }
  .meta { flex: 1; min-width: 0; }
  .name { font-weight: 600; color: #f0f6fc; text-decoration: none; }
  .name:hover { text-decoration: underline; }
  .sub { color: #8b949e; font-size: 12px; margin-top: 2px; }
  .bio { margin-top: 4px; white-space: pre-wrap; word-break: break-word; }
  .badge { display: inline-block; font-size: 11px; padding: 1px 8px; border-radius: 999px;
           border: 1px solid #30363d; margin-left: 6px; }
  .b-Company { background: #1f6feb33; color: #79c0ff; border-color: #1f6fef66; }
  .b-Influencer { background: #a371f733; color: #d2a8ff; border-color: #a371f766; }
  .b-Media { background: #d2992233; color: #e3b341; border-color: #d2992266; }
  .b-Friend { background: #3fb95033; color: #56d364; border-color: #3fb95066; }
  .b-Unclassified { background: #484f5833; color: #a8b1ba; border-color: #484f5866; }
  .hidden { display: none !important; }
  section { margin-top: 22px; }
  h2 { font-size: 14px; color: #f0f6fc; border-bottom: 1px solid #21262d; padding-bottom: 6px; }
  .saved-status { font-size: 11px; padding: 1px 8px; border-radius: 999px; }
  .s-ok { background: #3fb95033; color: #56d364; }
  .s-unavailable { background: #f8514933; color: #ff7b72; }
  .s-pending { background: #484f5833; color: #a8b1ba; }
  select.badge { margin-left: 6px; font-size: 11px; padding: 1px 6px; border-radius: 999px;
                 border: 1px solid #30363d; background: #161b22; color: #c9d1d9; cursor: pointer; }
  .statgrid { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 10px; margin-bottom: 16px; }
  .statcard { background: #161b22; border: 1px solid #21262d; border-radius: 8px; padding: 12px 14px; }
  .statcard .num { font-size: 22px; font-weight: 700; color: #f0f6fc; }
  .statcard .lbl { color: #8b949e; font-size: 12px; }
  .progress { height: 10px; border-radius: 999px; overflow: hidden; display: flex; background: #21262d; margin: 10px 0; }
  .progress .p-ok { background: #3fb950; }
  .progress .p-pending { background: #484f58; }
  .progress .p-unavail { background: #f85149; }
  .check { color: #8b949e; font-size: 13px; margin: 6px 0; }
  .check .ok { color: #56d364; font-weight: 700; }
  .check .warn { color: #e3b341; font-weight: 700; }
  button.more { background: #161b22; color: #79c0ff; border: 1px solid #30363d;
                padding: 8px 16px; border-radius: 6px; cursor: pointer; margin: 10px 0; }
  footer { color: #484f58; font-size: 12px; text-align: center; padding: 24px; }
</style>
</head>
<body>
<header>
  <h1 id="title">__TITLE__</h1>
  <div class="tabs">
    <button data-tab="status" class="active">Status</button>
    <button data-tab="contacts">Contacts</button>
    <button data-tab="saved">Saved posts</button>
    <button data-tab="posts">Posts</button>
    <a class="tablink" href="messages-gallery.html">Messages gallery ↗</a>
  </div>
  <input type="search" id="search" placeholder="Search username, name, bio…">
</header>
<main>
  <div id="tab-status">
    <div class="statgrid" id="statusCards"></div>
    <h2>Saved-media download progress</h2>
    <div class="progress" id="savedProgress"></div>
    <div class="stat" id="savedProgressText"></div>
    <h2>Deletion readiness</h2>
    <div id="readiness"></div>
  </div>
  <div id="tab-contacts" class="hidden">
    <div class="filters" id="catFilters"></div>
    <div class="stat" id="contactStat"></div>
    <div id="contactList"></div>
  </div>
  <div id="tab-saved" class="hidden">
    <div class="filters" id="savedFilters"></div>
    <div class="stat" id="savedStat"></div>
    <div id="savedList"></div>
    <button class="more hidden" id="savedMore">Show more</button>
  </div>
  <div id="tab-posts" class="hidden">
    <div class="stat" id="postStat"></div>
    <div id="postList"></div>
  </div>
</main>
<footer>Digital-Exit viewer — fully offline. Data embedded in this page.</footer>
<script>
const DATA = JSON.parse(atob("__DATA__"));
document.getElementById("title").textContent = "__TITLE_ESC__" +
  (DATA.accountName ? " — " + DATA.accountName : "");

const CATEGORIES = ["Company", "Influencer", "Media", "Friend", "Unclassified"];
let activeTab = "status";
let activeCats = new Set(CATEGORIES);
let savedFilter = "all";
let savedShown = 300;
let query = "";

// Manual category overrides, kept in localStorage so classification works
// fully offline (no API token needed).
let overrides = {};
try { overrides = JSON.parse(localStorage.getItem("de-categories") || "{}") || {}; } catch (e) { overrides = {}; }
function effCat(c) { return overrides[c.username] || c.category; }
function setCat(username, cat) {
  if (cat) { overrides[username] = cat; } else { delete overrides[username]; }
  try { localStorage.setItem("de-categories", JSON.stringify(overrides)); } catch (e) {}
  render();
}

document.querySelectorAll(".tabs button").forEach(b => {
  b.addEventListener("click", () => {
    document.querySelectorAll(".tabs button").forEach(x => x.classList.remove("active"));
    b.classList.add("active");
    activeTab = b.dataset.tab;
    ["status", "contacts", "saved", "posts"].forEach(t => {
      document.getElementById("tab-" + t).classList.toggle("hidden", t !== activeTab);
    });
  });
});

document.getElementById("search").addEventListener("input", e => { query = e.target.value.toLowerCase(); render(); });

// Delegated handler for the per-contact category dropdowns.
document.getElementById("contactList").addEventListener("change", e => {
  if (e.target && e.target.dataset && e.target.dataset.username) {
    setCat(e.target.dataset.username, e.target.value);
  }
});

function buildCatFilters() {
  const wrap = document.getElementById("catFilters");
  wrap.innerHTML = "";
  CATEGORIES.forEach(c => {
    const n = DATA.contacts.filter(x => effCat(x) === c).length;
    if (n === 0) return;
    const btn = document.createElement("button");
    btn.textContent = c + " (" + n + ")";
    btn.className = "b-" + c + " active";
    btn.addEventListener("click", () => {
      if (activeCats.has(c)) { activeCats.delete(c); btn.classList.remove("active"); }
      else { activeCats.add(c); btn.classList.add("active"); }
      render();
    });
    wrap.appendChild(btn);
  });
}

function buildSavedFilters() {
  const wrap = document.getElementById("savedFilters");
  wrap.innerHTML = "";
  const kinds = ["all", "ok", "pending", "unavailable"];
  kinds.forEach(k => {
    const n = k === "all" ? DATA.saved.length : DATA.saved.filter(s => s.status === k).length;
    if (k !== "all" && n === 0) return;
    const btn = document.createElement("button");
    btn.textContent = k + " (" + n + ")";
    if (savedFilter === k) btn.classList.add("active");
    btn.addEventListener("click", () => {
      savedFilter = k; savedShown = 300;
      wrap.querySelectorAll("button").forEach(x => x.classList.remove("active"));
      btn.classList.add("active");
      render();
    });
    wrap.appendChild(btn);
  });
}

function renderStatus() {
  const ok = DATA.saved.filter(s => s.status === "ok").length;
  const un = DATA.saved.filter(s => s.status === "unavailable").length;
  const pend = DATA.saved.length - ok - un;
  const cards = [
    ["Contacts", DATA.contacts.length],
    ["Posts", DATA.posts.length],
    ["Saved posts", DATA.saved.length],
    ["DM media files", (DATA.messages || []).length],
  ];
  document.getElementById("statusCards").innerHTML = cards.map(c =>
    '<div class="statcard"><div class="num">' + fmtNum(c[1]) + '</div><div class="lbl">' + c[0] + '</div></div>').join("");

  const total = Math.max(1, DATA.saved.length);
  document.getElementById("savedProgress").innerHTML =
    '<div class="p-ok" style="flex:' + ok + '"></div>' +
    '<div class="p-pending" style="flex:' + pend + '"></div>' +
    '<div class="p-unavail" style="flex:' + un + '"></div>';
  document.getElementById("savedProgressText").textContent =
    "downloaded " + ok + " · pending " + pend + " · unavailable " + un + " of " + DATA.saved.length +
    " (" + Math.round(100 * ok / total) + "%) · dataset generated " + (DATA.generatedAt || "unknown");

  const items = [
    ["Saved media drained", pend === 0,
      pend === 0 ? "every saved post is downloaded or accounted for"
                 : pend + " still pending — keep the runner going (./start-saved-downloads.sh)"],
    ["Unavailable saves", true,
      un === 0 ? "none yet" : un + " deleted or private on Meta's side — expected, non-blocking (03-media/unavailable.log)"],
    ["Dataset generated", !!DATA.generatedAt, DATA.generatedAt || "unknown"],
    ["Final integrity gate", false, "run 'digital-exit verify' — it re-checks every file on disk before you delete anything"],
  ];
  document.getElementById("readiness").innerHTML = items.map(it => {
    const mark = it[1] ? '<span class="ok">✓</span>' : '<span class="warn">…</span>';
    return '<div class="check">' + mark + ' <strong>' + escapeHtml(it[0]) + '</strong> — ' + escapeHtml(it[2]) + '</div>';
  }).join("");
}

function contactRow(c) {
  var initial = (c.displayName || c.username || "?").trim().charAt(0).toUpperCase() || "?";
  var q = escapeAttr((c.username + " " + (c.displayName||"") + " " + (c.bio||"")).toLowerCase());
  var eff = effCat(c);
  var opts = CATEGORIES.map(cat => '<option' + (cat === eff ? " selected" : "") + ' value="' + cat + '">' + cat + '</option>').join("");
  var s = '<div class="card" data-cat="' + eff + '" data-q="' + q + '">';
  s += '<div class="avatar">' + initial + '</div>';
  s += '<div class="meta">';
  s += '<a class="name" href="' + escapeAttr(c.url) + '" target="_blank" rel="noopener">' + escapeHtml(c.displayName || c.username) + '</a>';
  s += '<select class="badge b-' + eff + '" data-username="' + escapeAttr(c.username) + '" title="Classify this contact (saved offline in this browser)">' + opts + '</select>';
  if (overrides[c.username]) { s += '<span class="badge" title="manually set">✎</span>'; }
  if (c.verified) { s += '<span class="badge">✓ verified</span>'; }
  s += '<div class="sub">' + escapeHtml(c.kind) + ' · ' + fmtNum(c.followerCount) + ' followers · ' + fmtNum(c.followingCount) + ' following</div>';
  if (c.bio) { s += '<div class="bio">' + escapeHtml(c.bio) + '</div>'; }
  s += '</div></div>';
  return s;
}

function savedRow(s) {
  var q = escapeAttr((s.url + " " + (s.author||"")).toLowerCase());
  var out = '<div class="card" data-q="' + q + '">';
  out += '<div class="meta">';
  out += '<div class="sub"><span class="saved-status s-' + s.status + '">' + s.status + '</span>';
  if (s.kind) { out += ' · ' + escapeHtml(s.kind); }
  if (s.author) { out += ' · by ' + escapeHtml(s.author); }
  out += ' · saved ' + escapeHtml(s.savedAt||"") + '</div>';
  out += '<div class="sub"><a class="name" href="' + escapeAttr(s.url) + '" target="_blank" rel="noopener">' + escapeHtml(shortcode(s.url)) + '</a></div>';
  if (s.localPath) { out += '<div class="sub">local: ' + escapeHtml(s.localPath) + '</div>'; }
  out += '</div></div>';
  return out;
}

function postRow(p) {
  var q = escapeAttr((p.url + " " + (p.caption||"")).toLowerCase());
  var out = '<div class="card" data-q="' + q + '">';
  out += '<div class="meta">';
  out += '<div class="sub">' + escapeHtml(p.type) + ' · ' + escapeHtml(p.createdAt||"") + '</div>';
  out += '<div class="sub"><a class="name" href="' + escapeAttr(p.url) + '" target="_blank" rel="noopener">' + escapeHtml(shortcode(p.url)) + '</a></div>';
  if (p.caption) { out += '<div class="bio">' + escapeHtml(p.caption) + '</div>'; }
  out += '</div></div>';
  return out;
}

function fmtNum(n) {
  if (n >= 1e6) return (n/1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n/1e3).toFixed(1) + "K";
  return String(n || 0);
}
function shortcode(u) {
  const m = String(u || "").match(new RegExp("(p|reel|tv)/([A-Za-z0-9_-]+)"));
  return m ? m[2] : u;
}

function escapeHtml(s) { return String(s==null?"":s).replace(/[&<>]/g, c => ({ "&":"&amp;","<":"&lt;",">":"&gt;" }[c])); }
function escapeAttr(s) { return escapeHtml(s).replace(/"/g, "&quot;"); }

function render() {
  renderStatus();

  const list = document.getElementById("contactList");
  let shown = 0;
  const rows = DATA.contacts.filter(c => activeCats.has(effCat(c)));
  list.innerHTML = rows.map(contactRow).join("");
  Array.from(list.children).forEach(el => {
    const match = !query || el.dataset.q.indexOf(query) !== -1;
    el.classList.toggle("hidden", !match);
    if (match) shown++;
  });
  document.getElementById("contactStat").textContent =
    DATA.contacts.length + " contacts · showing " + shown +
    " · company=" + DATA.contacts.filter(c=>effCat(c)==="Company").length +
    " influencer=" + DATA.contacts.filter(c=>effCat(c)==="Influencer").length +
    " media=" + DATA.contacts.filter(c=>effCat(c)==="Media").length +
    " friend=" + DATA.contacts.filter(c=>effCat(c)==="Friend").length +
    " · click a label to re-classify (saved in this browser)";

  const filtered = DATA.saved.filter(s => savedFilter === "all" || s.status === savedFilter);
  const page = filtered.slice(0, savedShown);
  const sl = document.getElementById("savedList");
  sl.innerHTML = page.map(savedRow).join("");
  applyQuery(sl);
  const okCount = DATA.saved.filter(s=>s.status==="ok").length;
  document.getElementById("savedStat").textContent =
    filtered.length + " shown of " + DATA.saved.length + " saved · downloaded=" + okCount + " · " + summarizeSaved();
  const more = document.getElementById("savedMore");
  more.classList.toggle("hidden", filtered.length <= savedShown);
  more.textContent = "Show more (" + (filtered.length - savedShown) + " remaining)";
  more.onclick = () => { savedShown += 500; render(); };

  const pl = document.getElementById("postList");
  pl.innerHTML = DATA.posts.map(postRow).join("");
  applyQuery(pl);
  document.getElementById("postStat").textContent = DATA.posts.length + " posts indexed.";
}

function applyQuery(container) {
  const q = query;
  Array.from(container.children).forEach(el => el.classList.toggle("hidden", q && el.dataset.q.indexOf(q) === -1));
}

function summarizeSaved() {
  const p = DATA.saved.filter(s=>s.status==="pending").length;
  const u = DATA.saved.filter(s=>s.status==="unavailable").length;
  return "pending=" + p + " unavailable=" + u;
}

buildCatFilters();
buildSavedFilters();
render();
</script>
</body>
</html>
`

	esc := escapeJSString(title)
	html = strings.ReplaceAll(html, "__DATA__", dataB64)
	html = strings.ReplaceAll(html, "__TITLE__", esc)
	html = strings.ReplaceAll(html, "__TITLE_ESC__", esc)
	return html
}
