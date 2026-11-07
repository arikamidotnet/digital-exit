// validate-viewer.js — runtime check for the generated offline viewer.
//
// The viewer is a single HTML file with inline JS and base64-embedded data, so it
// cannot be unit-tested like a normal module. This harness stubs just enough of the
// DOM to execute that inline script in Node, then asserts that it booted, rendered
// every record, and filters correctly. Expectations are derived from the embedded
// dataset, so the same check works on any archive (no fixture-specific constants).
//
//   node tools/validate-viewer.js [path-to-index.html]
//
// Exits 0 and prints VALIDATE_OK when everything passes; exits 1 with CHECK_FAILED
// otherwise. Known gap: static tab buttons come from the parsed HTML, which this stub
// does not provide, so querySelectorAll returns [] and tab switching is untested here.

const fs = require('fs');

const htmlPath = process.argv[2] || '/tmp/de-test/05-output/index.html';
const html = fs.readFileSync(htmlPath, 'utf8');

const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) { console.error('FAIL: no inline <script> block found'); process.exit(1); }
const dataMatch = html.match(/atob\("([^"]*)"\)/);
if (!dataMatch) { console.error('FAIL: no base64 payload (atob("…")) found'); process.exit(1); }

const js = scriptMatch[1];
const data = JSON.parse(Buffer.from(dataMatch[1], 'base64').toString('utf8'));

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
    removeChild(c) { const i = this.children.indexOf(c); if (i >= 0) this.children.splice(i, 1); },
    setAttribute(k, v) { this.dataset[k] = v; }, focus() {},
    set value(v) { this._value = v; }, get value() { return this._value; },
    addEventListener(t, fn) { (this._listeners[t] = this._listeners[t] || []).push(fn); },
    dispatch(t, ev) { (this._listeners[t] || []).forEach(fn => fn(ev)); },
    set textContent(v) { this._text = v; }, get textContent() { return this._text; },
    set innerText(v) { this._text = v; }, get innerText() { return this._text; },
    // The viewer renders rows as HTML strings, so re-derive child "cards" from the
    // markup and keep their data-q attribute — that is what search filtering toggles.
    set innerHTML(v) {
      this._html = v;
      const cards = v.match(/class="card"[^>]*data-q="([^"]*)"/g) || [];
      el.children = cards.map(attr => {
        const child = makeEl();
        child.dataset.q = (attr.match(/data-q="([^"]*)"/) || [])[1] || '';
        return child;
      });
    },
    get innerHTML() { return this._html; },
  };
  return el;
}

const ids = {};
global.document = {
  getElementById: (id) => { if (!ids[id]) ids[id] = makeEl(); return ids[id]; },
  createElement: () => makeEl(),
  querySelectorAll: () => [],
};
global.atob = (s) => Buffer.from(s, 'base64').toString('utf8');

eval(js); // self-boots: builds filters and renders every list

const failures = [];
function check(label, actual, expected) {
  const pass = String(actual) === String(expected);
  console.log(`${pass ? 'ok  ' : 'FAIL'} ${label}: ${JSON.stringify(actual)}${pass ? '' : ' (expected ' + JSON.stringify(expected) + ')'}`);
  if (!pass) failures.push(label);
}

const rowCount = (el) => el.children.length;
const visibleCount = (el) => el.children.filter(c => !c.classList.contains('hidden')).length;

// 1. Every record in the embedded dataset must appear as a row.
check('contacts rows', rowCount(ids.contactList), data.contacts.length);
check('saved rows', rowCount(ids.savedList), data.saved.length);
check('posts rows', rowCount(ids.postList), data.posts.length);
check('contactStat at boot', ids.contactStat.textContent.includes(`showing ${data.contacts.length}`), true);

// 2. Search must narrow to exactly the records whose searchable text contains the query.
const haystack = (c) => `${c.username} ${c.displayName || ''} ${c.bio || ''}`.toLowerCase();
const queries = [];
if (data.contacts.length > 0) {
  queries.push(data.contacts[0].username.toLowerCase());
  const withBio = data.contacts.find(c => (c.bio || '').trim() !== '');
  if (withBio) queries.push(withBio.bio.trim().split(/\s+/)[0].toLowerCase());
}

for (const q of queries) {
  const expected = data.contacts.filter(c => haystack(c).includes(q)).length;
  const search = ids.search;
  search.value = q;
  search.dispatch('input', { target: search });
  check(`search "${q}" visible contacts`, visibleCount(ids.contactList), expected);
  check(`search "${q}" stat line`, ids.contactStat.textContent.includes(`showing ${expected}`), true);
}

// 3. Clearing the box must restore every row.
const search = ids.search;
search.value = '';
search.dispatch('input', { target: search });
check('cleared search restores rows', visibleCount(ids.contactList), data.contacts.length);

if (failures.length) {
  console.log(`CHECK_FAILED (${failures.length}): ${failures.join(', ')}`);
  process.exit(1);
}
console.log('VALIDATE_OK');
