package main

// renderWebUI returns the single-page localhost web client. Everything is
// vanilla JS fetching the JSON API — no build step, no external assets.
func renderWebUI() string {
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>digital-exit</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; font: 14px/1.5 -apple-system, Segoe UI, Roboto, Helvetica, Arial, sans-serif;
         background: #0d1117; color: #c9d1d9; }
  header { padding: 14px 20px; border-bottom: 1px solid #21262d; display: flex;
           align-items: center; gap: 12px; flex-wrap: wrap; position: sticky; top: 0;
           background: #0d1117; z-index: 5; }
  h1 { font-size: 16px; margin: 0; color: #f0f6fc; flex: 1; min-width: 180px; }
  h2 { font-size: 14px; color: #f0f6fc; border-bottom: 1px solid #21262d; padding-bottom: 6px; margin-top: 24px; }
  .tabs { display: flex; gap: 6px; flex-wrap: wrap; }
  .tabs button { background: #161b22; color: #c9d1d9; border: 1px solid #30363d;
                 padding: 6px 12px; border-radius: 6px; cursor: pointer; font-size: 13px; }
  .tabs button.active { background: #238636; border-color: #238636; color: #fff; }
  main { padding: 16px 20px; max-width: 1100px; margin: 0 auto; }
  .statgrid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 10px; margin-bottom: 16px; }
  .statcard { background: #161b22; border: 1px solid #21262d; border-radius: 8px; padding: 12px 14px; }
  .statcard .num { font-size: 22px; font-weight: 700; color: #f0f6fc; }
  .statcard .lbl { color: #8b949e; font-size: 12px; }
  .progress { height: 10px; border-radius: 999px; overflow: hidden; display: flex; background: #21262d; margin: 10px 0; }
  .progress .p-ok { background: #3fb950; }
  .progress .p-pending { background: #484f58; }
  .progress .p-unavail { background: #f85149; transition: flex .4s; }
  .row { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; margin: 8px 0; }
  button.act { background: #21262d; color: #c9d1d9; border: 1px solid #30363d;
               padding: 7px 14px; border-radius: 6px; cursor: pointer; font-size: 13px; }
  button.act:hover { border-color: #8b949e; }
  button.act.go { background: #238636; border-color: #238636; color: #fff; }
  button.act.stop { background: #b62324; border-color: #b62324; color: #fff; }
  button.act:disabled { opacity: .5; cursor: default; }
  input[type=text], input[type=search] { background: #0d1117; color: #c9d1d9;
      border: 1px solid #30363d; padding: 7px 10px; border-radius: 6px; min-width: 260px; }
  .stat { color: #8b949e; font-size: 13px; margin: 8px 0; }
  pre { background: #161b22; border: 1px solid #21262d; border-radius: 8px; padding: 12px;
        overflow: auto; max-height: 340px; font-size: 12px; }
  .card { background: #161b22; border: 1px solid #21262d; border-radius: 8px; padding: 12px 14px;
          margin-bottom: 10px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 10px; }
  .tile { background: #161b22; border: 1px solid #21262d; border-radius: 8px; overflow: hidden;
          text-decoration: none; color: #8b949e; font-size: 11px; display: block; }
  .tile .ph { height: 110px; display: grid; place-items: center; background: #0d1117; font-size: 30px; }
  .tile img { width: 100%; height: 110px; object-fit: cover; display: block; }
  .tile .cap { padding: 6px 8px; word-break: break-all; }
  .s-ok { color: #56d364; } .s-pending { color: #a8b1ba; } .s-unavailable { color: #ff7b72; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { text-align: left; padding: 6px 8px; border-bottom: 1px solid #21262d; }
  th { color: #8b949e; font-weight: 600; }
  a { color: #79c0ff; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .crumb { color: #8b949e; font-size: 13px; margin-bottom: 8px; word-break: break-all; }
  .hidden { display: none !important; }
  .authbox { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:10px 12px; margin:8px 0; }
  .authok { color:#56d364; } .authno { color:#f85149; }
  textarea { background:#0d1117; color:#c9d1d9; border:1px solid #30363d; padding:6px; border-radius:6px; width:100%; font-family:monospace; font-size:12px; }
  footer { color: #484f58; font-size: 12px; text-align: center; padding: 24px; }
</style>
</head>
<body>
<header>
  <h1>digital-exit <span id="account" style="color:#8b949e;font-weight:400"></span></h1>
  <div class="tabs">
    <button data-tab="dash" class="active">Dashboard</button>
    <button data-tab="saved">Saved</button>
    <button data-tab="contacts">Contacts</button>
    <button data-tab="files">Files</button>
  </div>
</header>
<main>
  <section id="tab-dash">
    <div class="statgrid" id="cards"></div>
    <h2>Saved-media downloads</h2>
    <div class="progress" id="prog"></div>
    <div class="stat" id="progText"></div>
    <div class="row">
      <button class="act go" id="btnStart">Start runner</button>
      <button class="act stop" id="btnStop">Stop runner</button>
      <button class="act" id="btnVerify">Run verify</button>
    </div>
    <pre id="verifyOut" class="hidden"></pre>
    <h2>Export archive (clean &amp; unzip)</h2>
    <div class="stat">Ingest unzips the export zips into 02-archive and re-normalizes. Clean wipes
      the staging tree first (zips stay in your backup folder — nothing is lost).</div>
    <div class="row">
      <input type="text" id="ingestPath" placeholder="path to instagram-*.zip" style="flex:1;min-width:300px">
      <button class="act" id="btnIngest">Unzip + ingest</button>
      <button class="act stop" id="btnClean">Clean staging</button>
    </div>
    <pre id="jobOut" class="hidden"></pre>

    <h2>Login to Instagram (for private/auth-walled saved posts)</h2>
    <div class="authbox" id="authBox">
      <div id="authStatus" class="stat" style="margin-bottom:8px"></div>
      <div class="row" style="margin-bottom:6px">
        <button class="act go" id="btnLoginInsta">Open Instagram login</button>
        <button class="act" id="btnClearCookies">Clear cookies</button>
      </div>
      <div class="row">
        <label style="font-size:12px;color:#8b949e">Upload cookies.txt (from browser extension after login):</label>
        <input type="file" id="cookieFile" accept=".txt,text/*">
        <button class="act" id="btnUploadCookies">Upload</button>
      </div>
      <div style="margin:6px 0 4px;font-size:12px;color:#8b949e">Or paste the cookies.txt content:</div>
      <textarea id="cookiePaste" rows="3" placeholder="# Netscape HTTP Cookie File&#10;.instagram.com	TRUE	/	TRUE	..."></textarea>
      <div class="row" style="margin-top:4px">
        <button class="act" id="btnPasteCookies">Use pasted cookies</button>
      </div>
      <div class="row" style="margin-top:8px">
        <select id="browserSel" class="act">
          <option value="">-- select your browser (close it first) --</option>
          <option value="firefox">firefox</option>
          <option value="chrome">chrome</option>
          <option value="chromium">chromium</option>
          <option value="edge">edge</option>
          <option value="brave">brave</option>
          <option value="opera">opera</option>
        </select>
        <button class="act go" id="btnCaptureBrowser">Capture cookies from browser</button>
      </div>
      <div style="font-size:11px;color:#8b949e;margin-top:4px">Login in the tab above, then (after closing the browser) click Capture. This pulls the session cookie into the app.</div> 
      <div class="row" style="margin-top:8px">
        <label><input type="checkbox" id="retryOnStart"> Retry unavailable when starting runner</label>
        <button class="act go" id="btnRetryUnavailable">Re-queue unavailable now</button>
      </div>
    </div>
  </section>
  <section id="tab-saved" class="hidden">
    <div class="row">
      <input type="search" id="savedSearch" placeholder="Search author / shortcode…">
      <select id="savedFilter" class="act" style="background:#161b22">
        <option value="all">all</option><option value="ok">ok</option>
        <option value="pending">pending</option><option value="unavailable">unavailable</option>
      </select>
      <span class="stat" id="savedStat"></span>
    </div>
    <div class="grid" id="savedGrid"></div>
    <div class="row"><button class="act hidden" id="savedMore">Show more</button></div>
  </section>
  <section id="tab-contacts" class="hidden">
    <div class="row">
      <input type="search" id="contactSearch" placeholder="Search username / bio…">
      <span class="stat" id="contactStat"></span>
    </div>
    <table id="contactTable"><thead>
      <tr><th>User</th><th>Kind</th><th>Followers</th><th>Category</th></tr>
    </thead><tbody></tbody></table>
    <div class="row"><button class="act hidden" id="contactMore">Show more</button></div>
  </section>
  <section id="tab-files" class="hidden">
    <div class="crumb" id="crumb"></div>
    <table id="fileTable"><thead>
      <tr><th>Name</th><th style="width:90px">Size</th><th style="width:60px"></th></tr>
    </thead><tbody></tbody></table>
  </section>
</main>
<footer>digital-exit web UI — localhost only. Offline viewer: 05-output/index.html</footer>
<script>
"use strict";
var DATA = null, STATUS = null, tab = "dash", savedShown = 60, contactShown = 200;
var cwd = "";

var CATS = ["Company", "Influencer", "Media", "Friend", "Unclassified"];

function setTab(name) {
  tab = name;
  document.querySelectorAll(".tabs button").forEach(function (x) {
    x.classList.toggle("active", x.dataset.tab === name);
  });
  ["dash", "saved", "contacts", "files"].forEach(function (t) {
    document.getElementById("tab-" + t).classList.toggle("hidden", t !== name);
  });
  if (name === "files") browse(cwd);
  if (location.hash !== "#" + name) history.replaceState(null, "", "#" + name);
  render();
}
document.querySelectorAll(".tabs button").forEach(function (b) {
  b.addEventListener("click", function () { setTab(b.dataset.tab); });
});

function get(url) { return fetch(url).then(function (r) { return r.json(); }); }
function post(url, body) {
  return fetch(url, { method: "POST", headers: { "Content-Type": "application/json" },
                      body: body ? JSON.stringify(body) : "{}" })
    .then(function (r) { return r.json().then(function (j) { if (!r.ok) throw new Error(j.error || r.status); return j; }); });
}
function fmtBytes(n) {
  if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + " MB";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + " KB";
  return n + " B";
}
function esc(s) { return String(s == null ? "" : s).replace(/[&<>"]/g, function (c) {
  return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]; }); }
function shortcode(u) { var m = String(u || "").match(/(p|reel|tv)\/([A-Za-z0-9_-]+)/); return m ? m[2] : u || ""; }

function load() {
  Promise.all([get("/api/data"), get("/api/status")]).then(function (rs) {
    DATA = rs[0]; STATUS = rs[1];
    document.getElementById("account").textContent = DATA.accountName ? "— " + DATA.accountName : "";
    render();
  });
}

function render() {
  if (!DATA) return;
  renderDash();
  if (tab === "saved") renderSaved();
  if (tab === "contacts") renderContacts();
}

function renderDash() {
  var c = [["Contacts", DATA.contacts.length], ["Posts", DATA.posts.length],
           ["Saved", STATUS.savedTotal], ["DM media", DATA.messages.length],
           ["Staging", STATUS.archive ? "present" : "empty"]];
  document.getElementById("cards").innerHTML = c.map(function (x) {
    return '<div class="statcard"><div class="num">' + x[1] + '</div><div class="lbl">' + x[0] + '</div></div>';
  }).join("");

  var ok = STATUS.savedOk, un = STATUS.savedUnavail, pend = STATUS.savedPending;
  var total = Math.max(1, STATUS.savedTotal);
  document.getElementById("prog").innerHTML =
    '<div class="p-ok" style="flex:' + ok + '"></div>' +
    '<div class="p-pending" style="flex:' + pend + '"></div>' +
    '<div class="p-unavail" style="flex:' + un + '"></div>';
  var f = STATUS.fetch;
  var run = f && f.running;
  document.getElementById("progText").textContent =
    "downloaded " + ok + " · pending " + pend + " · unavailable " + un + " of " + STATUS.savedTotal +
    " (" + Math.round(100 * ok / total) + "%)" +
    (run ? " · RUNNER ACTIVE pid " + f.pid + ", pass " + f.pass + ", " + f.newThisRun + " this run, last: " + (f.lastAction || "…")
         : " · runner idle (Start runner to resume)");

  var job = STATUS.job || {};
  var jo = document.getElementById("jobOut");
  if (job.kind) {
    jo.classList.remove("hidden");
    jo.textContent = (job.running ? "[running] " : "[done] ") + job.kind + "\\n" + (job.log || "") + (job.err ? "\\nERROR: " + job.err : "");
  } else { jo.classList.add("hidden"); }
  document.getElementById("btnStart").disabled = !!run;
  document.getElementById("btnStop").disabled = !run;

  // Auth / cookie status for private saved posts
  var a = STATUS.auth || {};
  var hasTxt = !!a.cookiesTxt;
  var hasBr  = a.cookiesBrowser && a.cookiesBrowser.length > 0;
  var authEl = document.getElementById("authStatus");
  if (authEl) {
    var parts = [];
    parts.push(hasTxt ? ('<span class="authok">cookies.txt present</span> (' + (a.cookiesTxtSize||0) + ' bytes)') : '<span class="authno">no cookies.txt</span>');
    if (hasBr) parts.push('browser: <span class="authok">' + esc(a.cookiesBrowser) + '</span>');
    authEl.innerHTML = 'Instagram auth: ' + parts.join(' · ') + ' — log in below, then retry unavailable items.';
  }
}

function savedFiltered() {
  var q = document.getElementById("savedSearch").value.toLowerCase();
  var st = document.getElementById("savedFilter").value;
  return DATA.saved.filter(function (s) {
    if (st !== "all" && s.status !== st) return false;
    if (q && (s.url + " " + (s.author || "")).toLowerCase().indexOf(q) === -1) return false;
    return true;
  });
}

function savedThumb(s) {
  var p = s.localPath;
  if (!p || s.status !== "ok") {
    var icon = s.status === "unavailable" ? "🚫" : "⏳";
    return '<div class="ph">' + icon + '</div>';
  }
  if (/\\.(jpg|jpeg|png|webp|gif)$/i.test(p)) return '<img loading="lazy" src="/file?path=' + encodeURIComponent(p) + '">';
  if (/\\.mp4$/i.test(p)) return '<div class="ph">🎬</div>';
  if (/\\.(m4a|mp3|ogg|wav)$/i.test(p)) return '<div class="ph">🎧</div>';
  return '<div class="ph">📄</div>';
}

function renderSaved() {
  var list = savedFiltered();
  var page = list.slice(0, savedShown);
  document.getElementById("savedStat").textContent = page.length + " / " + list.length + " shown";
  document.getElementById("savedGrid").innerHTML = page.map(function (s) {
    var st = '<span class="s-' + s.status + '">' + s.status + '</span>';
    var inner = savedThumb(s) + '<div class="cap">' + st + " · " + esc(s.author || shortcode(s.url)) + '</div>';
    var open = (s.localPath && s.status === "ok") ? "/file?path=" + encodeURIComponent(s.localPath) : s.url;
    return '<a class="tile" href="' + esc(open) + '" target="_blank" rel="noopener">' + inner + '</a>';
  }).join("");
  var more = document.getElementById("savedMore");
  more.classList.toggle("hidden", list.length <= savedShown);
  more.onclick = function () { savedShown += 120; renderSaved(); };
}

function renderContacts() {
  var q = document.getElementById("contactSearch").value.toLowerCase();
  var list = DATA.contacts.filter(function (c) {
    return !q || (c.username + " " + (c.displayName || "") + " " + (c.bio || "")).toLowerCase().indexOf(q) !== -1;
  });
  var page = list.slice(0, contactShown);
  document.getElementById("contactStat").textContent = page.length + " / " + list.length + " shown";
  document.querySelector("#contactTable tbody").innerHTML = page.map(function (c) {
    var opts = CATS.map(function (cat) {
      return '<option' + (cat === c.category ? " selected" : "") + '>' + cat + '</option>';
    }).join("");
    return '<tr><td><a href="' + esc(c.url) + '" target="_blank" rel="noopener">' + esc(c.username || c.displayName) + '</a></td>' +
      '<td>' + esc(c.kind) + '</td><td>' + (c.followerCount || "—") + '</td>' +
      '<td><select class="act" data-user="' + esc(c.username) + '">' + opts + '</select></td></tr>';
  }).join("");
  var more = document.getElementById("contactMore");
  more.classList.toggle("hidden", list.length <= contactShown);
  more.onclick = function () { contactShown += 400; renderContacts(); };
}

document.getElementById("contactTable").addEventListener("change", function (e) {
  var user = e.target.dataset.user;
  if (!user) return;
  post("/api/contact/category", { username: user, category: e.target.value })
    .then(function () { return get("/api/data"); })
    .then(function (d) { DATA = d; });
});

document.getElementById("savedSearch").addEventListener("input", function () { savedShown = 60; renderSaved(); });
document.getElementById("savedFilter").addEventListener("change", function () { savedShown = 60; renderSaved(); });
document.getElementById("contactSearch").addEventListener("input", function () { contactShown = 200; renderContacts(); });

document.getElementById("btnStart").addEventListener("click", function () {
  var retry = document.getElementById("retryOnStart") && document.getElementById("retryOnStart").checked;
  post("/api/fetch/start", retry ? {retryUnavailable: true} : null).then(load).catch(function (e) { alert(e.message); });
});
document.getElementById("btnStop").addEventListener("click", function () {
  post("/api/fetch/stop").then(load).catch(function (e) { alert(e.message); });
});
document.getElementById("btnVerify").addEventListener("click", function () {
  var out = document.getElementById("verifyOut");
  out.classList.remove("hidden"); out.textContent = "running…";
  post("/api/verify").then(function (j) { out.textContent = j.output; })
                     .catch(function (e) { out.textContent = "error: " + e.message; });
});
document.getElementById("btnIngest").addEventListener("click", function () {
  var p = document.getElementById("ingestPath").value.trim();
  if (!p) { alert("give the path to a zip or folder first"); return; }
  post("/api/ingest", { path: p }).then(load).catch(function (e) { alert(e.message); });
});
document.getElementById("btnClean").addEventListener("click", function () {
  if (!confirm("Wipe the 02-archive staging tree? Zips in your backup folder are untouched.")) return;
  post("/api/clean").then(load).catch(function (e) { alert(e.message); });
});

// --- Instagram auth / cookie capture (in-app login flow) ---
document.getElementById("btnLoginInsta").addEventListener("click", function () {
  window.open("https://www.instagram.com", "_blank", "noopener");
});

document.getElementById("btnClearCookies").addEventListener("click", function () {
  if (!confirm("Clear saved cookies (both cookies.txt and browser choice)?")) return;
  post("/api/cookies/clear").then(load).catch(function (e) { alert(e.message); });
});

document.getElementById("btnUploadCookies").addEventListener("click", function () {
  var f = document.getElementById("cookieFile");
  if (!f || !f.files || !f.files[0]) { alert("choose a cookies.txt file first"); return; }
  var fd = new FormData();
  fd.append("cookies", f.files[0]);
  fetch("/api/cookies", { method: "POST", body: fd })
    .then(function (r) { return r.json().then(function (j) { if (!r.ok) throw new Error(j.error || r.status); return j; }); })
    .then(load).catch(function (e) { alert(e.message); });
});

document.getElementById("btnPasteCookies").addEventListener("click", function () {
  var txt = document.getElementById("cookiePaste").value || "";
  if (!txt.trim()) { alert("paste some cookies.txt content first"); return; }
  post("/api/cookies", { content: txt }).then(load).catch(function (e) { alert(e.message); });
});

document.getElementById("btnUseBrowser").addEventListener("click", function () {
  var sel = document.getElementById("browserSel");
  var name = sel && sel.value ? sel.value.trim() : "";
  if (!name) { alert("select a browser first"); return; }
  post("/api/cookies/browser", { browser: name }).then(load).catch(function (e) { alert(e.message); });
});

document.getElementById("btnRetryUnavailable").addEventListener("click", function () {
  var start = confirm("Also start the download runner with --retry-unavailable now? (Cancel = just re-queue)");
  post("/api/saved/retry", { startRunner: start, stopFirst: true }).then(load).catch(function (e) { alert(e.message); });
});

document.getElementById("btnCaptureBrowser").addEventListener("click", function () {
  var sel = document.getElementById("browserSel");
  var name = sel && sel.value ? sel.value.trim() : "";
  if (!name) { alert("select your browser from the list first (close the browser completely before capturing)"); return; }
  if (!confirm("Capture cookies from " + name + "? Make sure you are logged into Instagram in that browser and have closed the browser window.")) return;
  post("/api/cookies/capture", { browser: name }).then(function (j) {
    alert((j.ok || "captured") + " (" + (j.bytes || 0) + " bytes)");
    return load();
  }).catch(function (e) { alert("capture failed: " + e.message + "\n\nTip: close the browser completely, then try again. Some browsers need --cookies-from-browser to be run while logged in."); });
});

function browse(path) {
  cwd = path || "";
  get("/api/fs?path=" + encodeURIComponent(cwd)).then(function (j) {
    var parts = cwd ? cwd.split("/") : [];
    var crumb = '<a href="#" data-p="">export</a>';
    parts.forEach(function (p, i) {
      var sub = parts.slice(0, i + 1).join("/");
      crumb += ' / <a href="#" data-p="' + esc(sub) + '">' + esc(p) + '</a>';
    });
    document.getElementById("crumb").innerHTML = crumb;
    document.querySelectorAll("#crumb a").forEach(function (a) {
      a.addEventListener("click", function (e) { e.preventDefault(); browse(a.dataset.p); });
    });
    j.entries.sort(function (a, b) { return (b.dir - a.dir) || a.name.localeCompare(b.name); });
    document.querySelector("#fileTable tbody").innerHTML = j.entries.map(function (f) {
      var sub = (cwd ? cwd + "/" : "") + f.name;
      var icon = f.dir ? "📁" : (f.isImg ? "🖼" : (f.isVid ? "🎬" : (f.isAud ? "🎧" : "📄")));
      var nameCell = f.dir
        ? '<a href="#" class="dir" data-p="' + esc(sub) + '">' + icon + " " + esc(f.name) + '</a>'
        : '<a href="/file?path=' + encodeURIComponent(sub) + '" target="_blank" rel="noopener">' + icon + " " + esc(f.name) + '</a>';
      return '<tr><td>' + nameCell + '</td><td>' + (f.dir ? "" : fmtBytes(f.size)) + '</td><td></td></tr>';
    }).join("");
    document.querySelectorAll("#fileTable a.dir").forEach(function (a) {
      a.addEventListener("click", function (e) { e.preventDefault(); browse(a.dataset.p); });
    });
  }).catch(function (e) { alert(e.message); });
}

// Deep-link: /#saved, /#contacts, /#files open the matching tab directly.
var initial = location.hash.replace("#", "");
if (["saved", "contacts", "files"].indexOf(initial) !== -1) setTab(initial);
load();
setInterval(function () { if (tab === "dash") get("/api/status").then(function (s) { STATUS = s; render(); }); }, 3000);
</script>
</body>
</html>
`;
}
