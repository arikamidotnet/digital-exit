# Digital-Exit Runbook

The process of attack for erasing an Instagram (and Facebook) footprint without
losing anything worth keeping. Follow it top to bottom; **do not delete anything
until Stage 7 passes.** The same run is repeatable for family and friends — each
person gets their own project root (`-root DIR`).

Everything below assumes you are in the project root, next to `runbook.md`.

## What the suite does

| Stage | Command | Automated? |
| --- | --- | --- |
| 1 Request + download official export | manual (browser) | no |
| 2 Ingest & normalize | `digital-exit ingest <zip\|dir>` | yes |
| 3 Saved-post media + profile enrichment | `digital-exit run [--auth]` | yes (yt-dlp / Graph API) |
| 4–5 Classify contacts | part of `run` | yes, heuristics — review them |
| 6 Emit offline viewer | part of `run` | yes |
| 7 Verify integrity + counts | `digital-exit verify` | yes |
| 8 Delete accounts | manual (browser) | no |

## Stage 0 — Environment

The Nix flake pins every tool the pipeline needs.

```sh
nix develop ./nix        # go, nodejs_24, pnpm, yt-dlp, ffmpeg, zip/unzip, file
# or build/run without entering a shell:
nix shell nixpkgs#go --command bash -c 'cd cli && go build -o ../digital-exit .'
```

Build the CLI once and use it from the project root:

```sh
cd cli && CGO_ENABLED=0 go build -o ../digital-exit . && cd ..
./digital-exit --help
```

`yt-dlp` is optional but strongly recommended: without it, saved posts are
recorded as `unavailable` instead of downloaded. `ffmpeg` lets yt-dlp merge
separate video/audio streams.

## Stage 1 — Request the official Instagram export (manual)

This is the only unavoidable manual step, and it has the longest lead time.

1. Instagram → Settings → **Accounts Center** → *Your information and permissions*
   → **Download your information** → *Request download*.
2. Choose **Complete copy** (or at minimum: Posts, Stories, Archived posts and
   stories, Followers and following, Saved posts and collections, Comments, Likes,
   Messages, Profile information).
3. Format **JSON**, quality **High**, date range **All time**.
4. *Export to device* → confirm the account email.
5. Watch for two emails: "your file is ready" (the download link **expires in 4
   days**) and, occasionally, a partial-export notice. Preparation can take
   anywhere from a few hours to ~30 days; most accounts finish in 1–3 days.
6. Download the ZIP promptly and put it in `01-request/`. Record the link and its
   expiry date in `01-request/request.log` — if the link lapses you must re-request.

> The export contains follower **usernames only** (no bios, no follower counts) and
> saved posts as a **link list with empty media fields**. That is why Stage 3 exists:
> it fetches the missing detail while the account is still alive.

## Stage 2 — Ingest and normalize

Drop the archive in whatever form you received it; the layout is auto-detected.

```sh
./digital-exit ingest ~/Downloads/instagram-user-20260101.zip
# an already-unzipped folder works too:
./digital-exit ingest 01-request/instagram-user-20260101
```

What happens: the archive is staged into `02-archive/`, the export root
(`instagram-user-DATE/`) is located by scanning for known markers rather than a
hard-coded path, and every recognized folder is normalized into
`04-processed/data.json`:

* `media/{posts,reels,stories,archived_posts}/YYYYMM/*.json` → posts (caption,
  timestamp, location, tagged users)
* `connections/followers_and_following/{followers_1.json,…,following.json}` → contacts
* `your_instagram_activity/saved/saved_posts.json` (+ collections) → saved links
* likes / comments / messages are detected and reported as gaps if unsupported

Parser quirks already handled: localized label names (e.g. `网址` for "URL"),
double-encoded UTF-8 in captions, `{count: N}` wrapper objects around follower
counts, and folder-name drift between archive versions. Nothing fails silently —
anything unrecognized is surfaced by Stage 7.

## Stage 3 — Saved-post media + profile enrichment

```sh
./digital-exit run              # yt-dlp for saved posts, no bio enrichment
./digital-exit run --auth       # also enrich follower bios/counts via Graph API
```

**Saved-post media.** Each permalink in the saved list is fetched with `yt-dlp`
into `03-media/saved/<shortcode>.<ext>`, best MP4 video or image. Failures are
retried up to 3 times, then logged to `03-media/unavailable.log` and marked
`unavailable` — the run never aborts on a dead/private post. Re-running `run`
skips anything already `ok`, so it is safe (and cheap) to retry overnight for
rate-limit recovery.

**Profile enrichment.** The official export omits bios and follower counts, which
the classifier needs. With `--auth`, each contact that still lacks a bio or count
is resolved through the Instagram Graph API (`graph.instagram.com/search`). Put the
token in `01-request/auth.json`:

```json
{ "access_token": "EAAG…" }
```

Enrichment is best-effort: a missing token, an expired grant, or a rate limit
leaves that contact untouched and logs it to `03-media/unavailable.log`. Expect
throttling on 1k–20k contacts — back off and re-run rather than hammering. If you
have no API access at all, skip `--auth`: classification still works from whatever
the export provides, just with lower confidence (more `Unclassified`).

## Stage 4–5 — Classification (review this)

Every follower **and** following entry becomes a contact, bucketed by first match:

1. **Media** — bio matches news/press/journalist vocabulary
2. **Influencer** — creator vocabulary (`content creator`, `photographer`, `artist`, …), or ≥ 50k followers
3. **Company** — external link present *and* (business vocabulary or ≥ 1k followers)
4. **Friend** — small account (< 2k), no link, empty bio
5. **Unclassified** — everything else

These are signals, not ground truth. To correct a bucket, edit the `category`
field in `04-processed/data.json` and re-run Stage 6 only:

```sh
./digital-exit run        # idempotent; media already downloaded is skipped
```

## Stage 6 — Offline viewer

`run` writes a single self-contained page to `05-output/index.html`: the dataset is
embedded as base64, so it opens straight from disk with no server and no network.

```sh
./digital-exit view       # xdg-open / open / rundll32, whichever applies
```

Three tabs (Contacts / Saved posts / Posts), a live search box over username +
display name + bio, per-category filter chips with counts, follower/following
totals, and download status badges on saved posts. Copy `05-output/` anywhere you
want it to survive (external drive, another machine) — it is the artifact you will
still be able to read in ten years.

## Stage 7 — Verify before deleting

```sh
./digital-exit verify
```

Checks: archive present; `data.json` round-trips byte-for-byte (catches truncated
or corrupt writes); contact/post/saved counts with the category breakdown; and
that every saved post marked `ok` still exists on disk. It prints exactly which
media files are missing.

Only proceed when it ends with:

```
Verification passed — archive is complete and ready for review/deletion.
```

If it warns about missing media, re-run Stage 3 before deleting. If counts look
short versus the export, that usually means a folder variant the scanner did not
recognize — inspect `02-archive/` and report it rather than accepting the numbers.

## Stage 8 — Deletion

### Instagram (delete)

1. Confirm you are signed in to the right account (Accounts Center lists all linked profiles).
2. **Accounts Center** → *Personal details* → *Account ownership and control* →
   **Deactivation or deletion** → choose the Instagram account → **Delete account**.
3. Pick a reason, re-enter the password, confirm **Continue**.
4. Note the date. Deletion has a **30-day grace period — logging in cancels it**.
   After that, Meta says content is purged over up to ~90 days from backup systems.
5. Deleting Instagram also deletes the linked **Threads** account.

Do not log back in during the grace period unless you changed your mind. If you
want a clean break, remove the saved password from your browser and password
manager after confirming the deletion is scheduled.

### Facebook (wipe only — no export needed)

1. Detach first: *Settings & privacy → Settings → Accounts Center → Accounts* →
   **Remove** Instagram (already gone by then) and unlink any apps under
   *Apps and websites*.
2. Bulk-delete what you care about while the account still exists: posts/activity
   via **Activity log → All activity → Manage all activity**, photos, tags.
3. Then **Accounts Center** → *Personal details* → *Account ownership and control*
   → **Deactivation or deletion** → choose the Facebook profile → **Delete**.
4. Same 30-day grace period; same rule that logging in cancels it.

If Messenger matters, note that deleting Facebook removes your Messenger presence
and its message history.

### WhatsApp (tips only — no export)

* Back up first if you want the history: *Settings → Chats → Chat backup*, and for
  a portable copy of an individual thread *Settings → Chats → Export chat* (email or
  save to device, with or without media).
* Deleting the account: *Settings → Account → Delete account* — this wipes local
  message history immediately (no grace period), unlike Instagram/Facebook.
* Delete the cloud backup separately, or it survives: Google Drive (Drive → Backups)
  or iCloud (Settings → Apple ID → iCloud → Manage Storage → Backups).
* If WhatsApp is linked to an Accounts Center profile, unlink it there first so the
  deletion does not cascade unexpectedly.

## Re-running and recovery

* Every stage is idempotent: `ingest` re-stages and re-normalizes; `run` skips media
  already downloaded and contacts already enriched.
* Keep `-root DIR` per person, e.g. `./digital-exit run -root ~/people/alice`.
* Rate-limited or half-finished Stage 3? Just run it again later — retries do not
  re-fetch completed items, and permanent failures accumulate in
  `03-media/unavailable.log` for manual review.
* Until the grace period ends you can always re-open `05-output/index.html` and
  still have the whole readable archive.

## Known limitations

* Enrichment uses the Graph API token path; there is no Playwright login session,
  so contacts without an API-visible profile stay thin (they land in `Unclassified`).
* `contacts.db` / SQLite is reserved but unused — `data.json` is the store of record.
* The viewer is generated by the Go binary as one embedded HTML file (vanilla JS),
  not a separate Vite/TypeScript project. That is deliberate: zero build step, zero
  network, and it cannot rot when npm does.

## Recreating this as an agent task

Hand an agent this prompt plus `runbook.md`:

> Archive my Instagram data export at `<path>` into `-root <dir>` using the
> digital-exit CLI in this repo: ingest it, fetch saved-post media with yt-dlp,
> classify followers/following into Company/Influencer/Media/Friend, emit the
> offline viewer, and run `verify`. Report the counts and anything listed in
> `unavailable.log`. **Stop before Stage 8** — do not delete any account; deletion
> is manual and requires my confirmation.
