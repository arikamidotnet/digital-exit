# digital-exit

Archive an Instagram data export, turn the follower/following set into a searchable
contacts database, download saved-post media, and hand you an offline viewer — then
tell you exactly how to delete the accounts without losing anything.

Stdlib-only Go (no modules to fetch, builds offline) plus one Nix flake for the
toolchain. Runs on Linux; no network needed except for `yt-dlp` and optional
Graph API enrichment.

## Install

```sh
# from this repo
nix profile install github:arikamidotnet/digital-exit

# or run without installing
nix run github:arikamidotnet/digital-exit -- --help
```

## Try it now (no real Instagram data needed)

There is a sample export committed in `tests/fixtures/`. Run this to see the whole
pipeline build, ingest, classify, emit the viewer **and** the message gallery, then
verify — all inside a throwaway temp directory:

```sh
nix shell nixpkgs#go --command bash tools/demo.sh
```

It prints where the generated `index.html` and `messages-gallery.html` live. This is
the fastest way to confirm the tool works before pointing it at your own export.

## Quickstart

```sh
# With the binary on PATH (via nix profile install or nix run):

digital-exit --help

# 1. Drop the export in — .zip or already-unzipped folder, layout auto-detected
#    (use -root to keep one tree per person / per export)
digital-exit ingest ~/Downloads/instagram-user-20260101.zip
# or
digital-exit ingest ~/Downloads/instagram-*.zip -root ./my-export

# 2. Background saved-post downloader (resumable, checkpoints, single-instance)
#    Create a tiny launcher next to your export root if you want (example below),
#    or just run the command directly.
digital-exit fetch --watch -root ./my-export

# 3. Full pipeline (ingest + media + classify + viewer) in one go:
digital-exit run -root ./my-export

# Message gallery (DM photos/videos/audio aggregated + renamed):
digital-exit gallery -root ./my-export
digital-exit view -root ./my-export   # opens 05-output/messages-gallery.html

# Localhost web UI (dashboard, runner controls, file manager, saved/contacts browsers):
digital-exit serve --open -root ./my-export

# Verify before you delete anything:
digital-exit verify -root ./my-export
```

Typical layout for one export (you create this directory tree):

```
my-export/
  01-request/          # put zips + optional cookies.txt here
  02-archive/          # staging (unzipped export)
  03-media/saved/      # downloaded saved posts
  04-processed/        # data.json + fetch status/lock
  05-output/           # index.html + messages-gallery.html
```

A minimal per-export launcher (put this next to your export root, adapt the path):

```fish
#!/usr/bin/env fish
set -l root (dirname (status filename))
exec digital-exit fetch -root $root --watch $argv
```

Use `-root DIR` to keep separate work for different people or exports.

## Layout (per export root)

* `01-request/` — zips + optional `cookies.txt` / `cookies-browser.txt` for private saved posts
* `02-archive/` — the staged (unzipped) official export
* `03-media/saved/` — saved-post media fetched by yt-dlp; `unavailable.log` for permanent failures
* `04-processed/data.json` — normalized dataset (contacts, posts, saved)
* `04-processed/fetch-status.json` — live progress for the background runner
* `05-output/index.html` — self-contained offline viewer
* `05-output/messages-gallery.html` — DM media aggregated + renamed

The program itself is just the binary + this repo. The export layout above is
created on first use with `-root` (or the current directory).

## What it produces

Contacts are bucketed as **Company / Influencer / Media / Friend** (with an
`Unclassified` fallback). The viewer is a single HTML file with the dataset
embedded as base64.

## Deletion

See [`runbook.md`](runbook.md) for the full Instagram/Facebook deletion workflow.
Always run `digital-exit verify` first.
