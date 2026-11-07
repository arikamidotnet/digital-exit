#!/usr/bin/env bash
# smoke.sh — end-to-end validation of the digital-exit pipeline against the committed
# fixture in tests/fixtures/. Run it after any change to ingest/parse/classify/viewer:
#
#   nix shell nixpkgs#go --command bash tools/smoke.sh
#
# It builds the CLI, ingests the fixture three ways (export folder, wrapper folder, and
# .zip when zip is available), runs enrichment + classification + viewer emission,
# verifies integrity, and executes the generated viewer in Node. Prints SMOKE_OK on
# success and exits non-zero on the first unexpected result.
#
# Requires: go (via nix), node for the viewer check. yt-dlp is optional — without it the
# saved posts are recorded as unavailable, which is what these assertions expect.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE="$ROOT/tests/fixtures/instagram-user-20260101"
BIN="$(mktemp -d)/digital-exit"
WORK="$(mktemp -d)"
FAILURES=0

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL %s\n' "$*" >&2; FAILURES=$((FAILURES + 1)); }

# expect <label> <needle> <file> — assert a fixed string appears in captured output.
expect() {
  if grep -qF -- "$2" "$3"; then
    log "ok   $1"
  else
    fail "$1: expected to find \"$2\" in $3"
    sed 's/^/     | /' "$3"
  fi
}

command -v go >/dev/null || { echo "go not on PATH — run: nix shell nixpkgs#go --command bash tools/smoke.sh" >&2; exit 2; }

log "== build =="
( cd "$ROOT/cli" && CGO_ENABLED=0 go build -o "$BIN" . && CGO_ENABLED=0 go vet ./... )
log "ok   built + vet clean: $BIN"

# ingest_check <label> <input path> — ingest into a throwaway root and assert counts.
# Expects the classic fixture (4 contacts, 1 post, 2 saves).
ingest_check() {
  local label="$1" input="$2" out="$WORK/$3"
  rm -rf "$WORK/root-$3"; mkdir -p "$WORK/root-$3"
  "$BIN" ingest "$input" -root "$WORK/root-$3" >"$out" 2>&1
  expect "$label contacts" "contacts: 4 (followers=2 following=2)" "$out"
  expect "$label posts"    "posts:    1" "$out"
  expect "$label saved"    "saved:    2" "$out"
}

# ingest_check_current — same assertions but for the current-format fixture, which uses
# Meta's connections-list (string_list_data) + label_values array shapes. Still produces
# 4 contacts / 1 post / 2 saves.
ingest_check_current() {
  local label="$1" input="$2" out="$WORK/$3"
  rm -rf "$WORK/root-$3"; mkdir -p "$WORK/root-$3"
  "$BIN" ingest "$input" -root "$WORK/root-$3" >"$out" 2>&1
  expect "$label contacts (current fmt)" "contacts: 4 (followers=2 following=2)" "$out"
  expect "$label posts   (current fmt)"    "posts:    1" "$out"
  expect "$label saved   (current fmt)"    "saved:    2" "$out"
}

log "== ingest: export folder =="
ingest_check "folder" "$FIXTURE" "dir"

log "== ingest: wrapper folder (root must be located by signature) =="
ingest_check "wrapper" "$ROOT/tests/fixtures" "wrap"

if command -v zip >/dev/null; then
  log "== ingest: .zip =="
  ZIP="$WORK/fixture.zip"
  ( cd "$ROOT/tests/fixtures" && zip -qr "$ZIP" instagram-user-20260101 )
  ingest_check "zip" "$ZIP" "zip"
else
  log "skip .zip case (no zip binary; add it via the nix shell)"
fi

log "== ingest: current-format fixture (string_list_data + label_values array) =="
ingest_check_current "current" "$ROOT/tests/fixtures/current-format" "cur"

log "== run: media + classify + viewer =="
RUN_OUT="$WORK/run.log"
"$BIN" run -root "$WORK/root-dir" >"$RUN_OUT" 2>&1
expect "classification buckets" "company=0 influencer=1 media=1 friend=2 unclassified=0" "$RUN_OUT"
expect "viewer emitted"         "05-output/index.html" "$RUN_OUT"
expect "message gallery emitted" "05-output/messages-gallery.html" "$RUN_OUT"

log "== message gallery runtime =="
if command -v node >/dev/null; then
  GALL_OUT="$WORK/gallery.log"
  if node "$ROOT/tools/validate-gallery.js" "$WORK/root-dir/05-output/messages-gallery.html" >"$GALL_OUT" 2>&1; then
    log "ok   message gallery rendered ($(cat "$GALL_OUT"))"
  else
    fail "message gallery validation failed"
    sed 's/^/     | /' "$GALL_OUT"
  fi
else
  log "skip message gallery runtime check (node not on PATH)"
fi

log "== verify =="
VERIFY_OUT="$WORK/verify.log"
"$BIN" verify -root "$WORK/root-dir" >"$VERIFY_OUT" 2>&1
expect "integrity round-trip" "integrity: OK" "$VERIFY_OUT"
# Smoke runs without yt-dlp/network, so the fixture's 2 saves stay pending and
# the deletion gate must report exactly that instead of waving the run through.
expect "verification gate (pending)" "saved items are still pending" "$VERIFY_OUT"

log "== viewer runtime =="
if command -v node >/dev/null; then
  VIEWER_OUT="$WORK/viewer.log"
  if node "$ROOT/tools/validate-viewer.js" "$WORK/root-dir/05-output/index.html" >"$VIEWER_OUT" 2>&1; then
    log "ok   viewer rendered + filtered correctly ($(grep -c '^ok' "$VIEWER_OUT") checks)"
  else
    fail "viewer validation failed"
    sed 's/^/     | /' "$VIEWER_OUT"
  fi
else
  log "skip viewer runtime check (node not on PATH)"
fi

log ""
if [ "$FAILURES" -gt 0 ]; then
  echo "SMOKE_FAILED ($FAILURES)"
  exit 1
fi
echo "SMOKE_OK"
