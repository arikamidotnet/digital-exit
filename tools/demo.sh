#!/usr/bin/env bash
# demo.sh — try digital-exit end-to-end with SAMPLE data, no real Instagram export needed.
#
#   nix shell nixpkgs#go --command bash tools/demo.sh
#
# It builds the CLI, ingests a small committed fixture (instagram-user-20260101), runs
# the full pipeline (ingest -> fetch media/enrichment -> classify -> viewer + message
# gallery), and verifies integrity. Everything lands in a throwaway temp root so nothing
# is written into the repo or your real export. Prints SMOKE_OK on success.
#
# Requires: go (via nix). yt-dlp is optional — without it saved posts are recorded as
# unavailable, which is fine for a demo.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE="$ROOT/tests/fixtures/instagram-user-20260101"
BIN="$(mktemp -d)/digital-exit"
DEMO_ROOT="$(mktemp -d)/demo-root"

# go is not usually on PATH outside the nix shell.
if ! command -v go >/dev/null 2>&1; then
  echo "go not on PATH — re-run inside the dev shell:" >&2
  echo "  nix shell nixpkgs#go --command bash tools/demo.sh" >&2
  exit 2
fi

echo "== building =="
( cd "$ROOT/cli" && CGO_ENABLED=0 go build -o "$BIN" . )
echo "ok   built: $BIN"

echo ""
echo "== running pipeline against sample fixture (isolated in $DEMO_ROOT) =="
"$BIN" ingest "$FIXTURE" -root "$DEMO_ROOT" 2>&1
echo ""
"$BIN" run -root "$DEMO_ROOT" 2>&1
echo ""
"$BIN" verify -root "$DEMO_ROOT" 2>&1

echo ""
echo "=== done ==="
echo "Outputs are in: $DEMO_ROOT"
echo "  viewer:       $DEMO_ROOT/05-output/index.html"
echo "  message gal.: $DEMO_ROOT/05-output/messages-gallery.html"
echo "Open the viewer with:  $BIN view -root $DEMO_ROOT"
echo ""
echo "Now try your own export:"
echo "  ./digital-exit ingest ~/Downloads/instagram-user-20260101.zip"
echo "  ./digital-exit run --auth"
echo "  ./digital-exit view"
