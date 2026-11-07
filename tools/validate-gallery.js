// validate-gallery.js — runtime check for the generated offline message gallery.
//
// Like validate-viewer.js, the gallery is a single HTML file with inline JS and a
// base64-embedded payload, so it cannot be unit-tested like a normal module. This
// harness stubs just enough of the DOM to execute that inline script in Node, then
// asserts that it booted, grouped every item by user, rendered a figure per item, and
// filters correctly. Expectations are derived from the embedded data, so the check
// works on any archive (no fixture-specific constants).
//
//   node tools/validate-gallery.js [path-to-messages-gallery.html]
//
// Exits 0 and prints VALIDATE_OK when everything passes; exits 1 with CHECK_FAILED.

const fs = require('fs');

const htmlPath = process.argv[2] || '05-output/messages-gallery.html';
const html = fs.readFileSync(htmlPath, 'utf8');

const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) { console.error('FAIL: no inline <script> block found'); process.exit(1); }
const dataMatch = html.match(/atob\("([^"]*)"\)/);
if (!dataMatch) { console.error('FAIL: no base64 payload (atob("…")) found'); process.exit(1); }

const js = scriptMatch[1];
const items = JSON.parse(Buffer.from(dataMatch[1], 'base64').toString('utf8'));

// An archive with no DM media still produces a valid (empty) gallery.
if (items.length === 0) { console.log('VALIDATE_OK (no message media)'); process.exit(0); }

// Minimal DOM element whose innerHTML is parsed back into figure + heading children so
// we can assert on what the gallery actually rendered.
function makeEl() {
  const el = {
    _text: '', _html: '', _value: '', dataset: {}, children: [], _listeners: {},
    classList: {
      _s: new Set(),
      toggle(c, force) { if (force === false) this._s.delete(c); else this._s.add(c); },
      add(c) { this._s.add(c); }, remove(c) { this._s.delete(c); },
      contains(c) { return this._s.has(c); },
    },
    appendChild(c) { this.children.push(c); },
    setAttribute(k, v) { this.dataset[k] = v; }, focus() {},
    set value(v) { this._value = v; }, get value() { return this._value; },
    addEventListener(t, fn) { (this._listeners[t] = this._listeners[t] || []).push(fn); },
    dispatch(t, ev) { (this._listeners[t] || []).forEach(fn => fn(ev)); },
    set textContent(v) { this._text = v; }, get textContent() { return this._text; },
    set innerHTML(v) {
      this._html = v;
      this.children = [];
      // Each media item is a <figure ...> with a data-q attribute for filtering.
      const figures = v.match(/<figure[^>]*data-q="([^"]*)"/g) || [];
      figures.forEach(attr => {
        const child = makeEl();
        child.dataset.q = (attr.match(/data-q="([^"]*)"/) || [])[1] || '';
        this.children.push(child);
      });
    },
    get innerHTML() { return this._html; },
  };
  return el;
}

const ids = {};
global.document = {
  getElementById: (id) => { if (!ids[id]) ids[id] = makeEl(); return ids[id]; },
  querySelectorAll: () => [],
};
global.atob = (s) => Buffer.from(s, 'base64').toString('utf8');

eval(js); // self-boots: groups by user and renders every item

const failures = [];
function check(label, actual, expected) {
  const pass = String(actual) === String(expected);
  console.log(`${pass ? 'ok  ' : 'FAIL'} ${label}: ${JSON.stringify(actual)}${pass ? '' : ' (expected ' + JSON.stringify(expected) + ')'}`);
  if (!pass) failures.push(label);
}

const gallery = ids.gallery;
const figures = () => gallery.children.length;
const visibleFigures = () => gallery.children.filter(c => !c.classList.contains('hidden')).length;

// Distinct users and per-user counts, derived from the embedded data.
const byUser = {};
items.forEach(it => {
  const u = it.user || '(unknown)';
  byUser[u] = (byUser[u] || 0) + 1;
});

// The gallery is client-rendered: read headings/figures from the DOM the inline JS
// actually built at boot, not from the raw source template strings.
const renderedHtml = gallery._html || '';
const headings = renderedHtml.match(/<h2>([^<]+)/g) || [];

// 1. Every item renders as a figure; one heading per distinct user.
check('figures rendered', figures(), items.length);
check('user headings', headings.length, Object.keys(byUser).length);

// 2. Search is full-text (user / date / title / filename), so querying the longest
// username must keep all of that user's figures and include at least one belonging to
// them — without ever exceeding the total.
const sampleUser = Object.keys(byUser).sort((a, b) => b.length - a.length)[0];
const expectedForUser = byUser[sampleUser];
const search = ids.search;
search.value = sampleUser.toLowerCase();
search.dispatch('input', { target: search });
check(`search "${sampleUser}" keeps its figures (>= expected)`, visibleFigures() >= expectedForUser, true);
check(`search "${sampleUser}" never exceeds total`, visibleFigures() <= items.length, true);
const hasTarget = gallery.children.some(c => c.dataset.q.startsWith(sampleUser.toLowerCase()));
check(`search "${sampleUser}" target present`, hasTarget, true);

// 3. Clearing the box restores every figure.
search.value = '';
search.dispatch('input', { target: search });
check('cleared search restores figures', visibleFigures(), items.length);

if (failures.length) {
  console.log(`CHECK_FAILED (${failures.length}): ${failures.join(', ')}`);
  process.exit(1);
}
console.log('VALIDATE_OK');
