# Agent Handover — digital-exit

This document lets another agent take over the `digital-exit` project without re-deriving context from scratch. It covers: what exists, how to build/test it, known limitations, and where work remains.

## Project at a glance

**Goal:** Archive an Instagram data export (posts, stories, saved-post media, followers/following), turn the follower set into a searchable contacts database classified as Company/Influencer/Media/Friend, emit an offline viewer, then guide deletion of Instagram + Facebook accounts without losing anything worth keeping. WhatsApp is tips-only.

**Repo layout:**
```
digital-exit/
  cli/          Go CLI (stdlib-only, CGO_ENABLED=0) — ingest, run, verify, view, gallery
  nix/          flake.nix + shell.nix pinning go, node, yt-dlp, ffmpeg, zip/unzip, file
  tools/        smoke.sh (end-to-end test), demo.sh (one-command user-testing walkthrough),
                validate-viewer.js (DOM-stub viewer check), validate-gallery.js (gallery check)
  tests/fixtures/instagram-user-20260101/   small synthetic export for CI + demos
  runbook.md    full deletion procedure + agent-task spec
  README.md     quickstart
  .gitignore    ignores local data dirs and built binaries
```

## Build & test (one-liners)

Go is not on the system PATH — use nix:

```sh
# Build the CLI
nix shell nixpkgs#go --command bash -c 'cd cli && CGO_ENABLED=0 go build -o ../digital-exit .'

# Full end-to-end smoke test against the committed fixture (build, ingest x3 shapes, run, verify, viewer)
nix shell nixpkgs#go nodejs_24 --command bash tools/smoke.sh

# Validate just the generated viewer HTML against any dataset
node tools/validate-viewer.js path/to/index.html
```

The flake also provides `zip`, `yt-dlp`, and `ffmpeg` for real runs.

## CLI usage

```sh
./digital-exit ingest <path>...   # one or more zips/folders; Meta's multi-zip export merges all
./digital-exit run [--auth]        # yt-dlp saved media + classify + emit viewer + gallery
./digital-exit gallery             # aggregate every DM photo/video/audio into 03-media/messages/ and emit messages-gallery.html
./digital-exit fetch --watch       # background runner: drains the whole saved queue,
                                   #   retry passes every 10m, single-instance flock,
                                   #   SIGINT-safe checkpoints, 04-processed/fetch-status.json
./digital-exit serve [--open]      # localhost web UI (:8080): live dashboard, runner
                                   #   start/stop, verify, saved gallery with local thumbs,
                                   #   contacts editor (persists to data.json), file manager
                                   #   over the export root, clean staging + re-unzip zips
./digital-exit verify              # integrity + wipe gate (NOT READY while saves are pending)
./digital-exit view                # open 05-output/index.html (Status tab = live dashboard,
                                   #   Messages gallery link, localStorage category overrides)
```

The detached launcher lives in the export root: `start-saved-downloads.sh`
(fish: setsid --fork + flock + nix shell yt-dlp/ffmpeg → `fetch --watch`).

`-root DIR` isolates per-person runs. `--auth` enables Graph API enrichment (token at `01-request/auth.json`).

## What's been built and validated

- **Ingest** (`ingest.go`): accepts `.zip`, `.tar.gz`, or folders; merges multiple zips into one staging tree; auto-detects the export root by signature (not path); normalizes contacts, saved posts, and media. Handles Meta's two follower-list schemas:
  - `{followers:[{username,biography,...}]}` — older/enriched shape
  - `[{string_list_data:[{value,href,timestamp}]}]` — current connections-list format (usernames only)
- **Saved posts** (`parse.go`): reads `saved_posts.json`/`saved_collections.json` from any location; handles both map and array forms of `label_values`/`string_map_data`; dedupes by permalink.
- **Posts** (`ingest.go`): parses JSON sidecars when present, falls back to enumerating raw media files (`.jpg/.mp4/.webp/etc.) when Meta ships no metadata — derives shortcode from filename, type from directory name.
- **Classification** (`classify.go`): Company/Influencer/Media/Friend/Unclassified via bio-vocabulary + follower-count + external-link heuristics across followers AND following.
- **Media fetch** (`media.go`): yt-dlp per permalink with 3 retries → `unavailable.log`; idempotent (skips already-downloaded). Without yt-dlp, records saves as unavailable.
- **Enrichment** (`media.go`): optional Graph API call resolves bios/counts for contacts missing them; best-effort, logs failures.
- **Viewer** (`viewer.go`): single self-contained HTML file with base64-embedded dataset and vanilla JS (tabs, live search, category filters). No build step, no npm install — opens from disk forever.
- **Message gallery** (`messages.go`): `gallery` command (also run by `run`) aggregates every DM photo/video/audio from `your_instagram_activity/messages/` into one folder as `<date>_<user>_<stem>.<ext>` — date from each media's own `creation_timestamp`, user from the message sender — and emits a single offline HTML gallery (`05-output/messages-gallery.html`) grouped by user, sorted by date. Non-destructive (copies from archive) and idempotent. External CDN-URI references that Meta did not ship as local files are kept as links rather than errors.
- **Verify** (`verify.go`): archive presence, data.json round-trip integrity, counts + category breakdown, on-disk confirmation of every downloaded save and aggregated message file. Deletion is gated on this passing.

### Real-data validation (Oct 2026)

Tested against the user's actual Instagram multi-zip export (6 zips merged):
```
contacts: 2004 (followers=2003 following=1)
posts:    6797
saved:    3360 total (downloaded=0 unavailable=3360, on-disk=0, missing=0)
Verification passed — archive is complete and ready for review/deletion.
```

The fixture test (`tools/smoke.sh`) also passes with `SMOKE_OK`.

## Known limitations & design decisions

1. **No TypeScript viewer** — the plan mentioned a Vite/TS app; instead, `cli/viewer.go` emits one HTML file (base64 data + vanilla JS). Deliberate: zero build step, survives toolchain rot, opens offline. Recorded as a divergence in `runbook.md`.
2. **Enrichment is API-token only** — no Playwright browser login automation. Without `--auth`, contacts land in `Unclassified` (no bios/counts available from the export). The official export ships usernames only; enrichment must happen before deletion while the account is alive.
3. **`contacts.db`/SQLite reserved but unused** — `04-processed/data.json` is the store of record. SQLite path exists for a future migration.
4. **Saved-post media download untested with real yt-dlp** — validated only in its "binary absent" branch. The retry/unavailable logic wants one run against live permalinks before trusting it on 3k+ saves. `tools/smoke.sh` covers the no-ytdlp path; add a `--with-yt-dlp` variant if needed.
5. **Posts from raw media files have synthetic shortcodes** (filename-derived) and placeholder URLs when no JSON sidecars exist — good enough for listing/search, but caption/timestamp/tagged-user fields will be empty.

## Where work remains

1. **yt-dlp integration test**: run `digital-exit run --auth` against a handful of live saved-post permalinks to confirm the download path works end-to-end (currently only validated as "binary absent → unavailable"). Requires yt-dlp + network access, and an Instagram account still alive with saves pointing to non-deleted content.
2. **Enrichment at scale**: test `--auth` Graph API enrichment against 1k+ contacts — expect rate limiting; the backoff/retry logic should be validated, not just unit-reasoned about.
3. **Optional: standalone TS viewer**: if the user changes their mind on decision #1 above, create `web/` with a Vite + TypeScript app that reads `04-processed/data.json` and replicates the current HTML features (tabs, search, category filters). The embedded viewer would then become an alternative output format rather than the only one.
4. **Threads/Facebook export**: the real export includes `your_instagram_activity/threads/` data — currently ignored by the parser. Add threads normalization if that's in scope.

## For whoever picks this up next

- Read `runbook.md` before touching anything account-related. Stage 7 (`verify`) MUST pass before any deletion (Stage 8).
- All code is Go stdlib only; no external modules to fetch. Build with `CGO_ENABLED=0`.
- The fixture at `tests/fixtures/instagram-user-20260101/` uses the *old* export schema (`{followers:[{username:...}]}` + JSON post sidecars). If you add a test case for the current Meta format, use your real multi-zip export as the reference shape.
- `tools/smoke.sh` is the single entry point for regression testing — extend it rather than adding ad-hoc scripts.
