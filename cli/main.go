// Command digital-exit archives an Instagram data export, builds a searchable
// contacts database from the follower/following set, downloads saved-post media,
// and emits an offline viewer. See runbook.md for the full process of attack.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const usage = `digital-exit — Instagram footprint archive suite

USAGE:
  digital-exit <command> [options]

COMMANDS:
  ingest <path>...   Drop an exported archive (raw .zip, unzipped folder, or several
                  zips) in and normalize every known folder into 04-processed/.
                  No fixed path or schema required — the layout is auto-detected.
  run             Run the full pipeline: ingest -> fetch media/enrichment ->
                  classify contacts -> emit viewer. Add --auth to also launch the
                  authenticated browser layer (saved-post media + follower bios).
  fetch           Download saved-post media (resumable, in-order). Without
                  --saved-limit the ENTIRE pending queue is processed. Add
                  --watch to loop retry passes until the queue drains — the
                  mode for the detached background runner. Avoids
                  re-downloading anything already on disk or marked ok.
  verify          Check counts vs the export, checksums and media integrity.
  gallery         Aggregate every DM photo/video/audio into one folder (renamed
                  <date>_<user>_<stem>.<ext>) and emit an offline HTML gallery at
                  05-output/messages-gallery.html.
  serve           Start the localhost web UI: live dashboard, runner start/stop,
                  saved gallery, contacts editor, file manager, clean/re-unzip.
                  Binds 127.0.0.1:8080 by default; add --open to auto-launch.
  view            Open the generated offline viewer at 05-output/index.html in a browser.

OPTIONS:
  -root DIR            Project root (default: current directory).
  --saved-limit N      When running 'run', fetch at most N *new* saved-post
                       media items this invocation (0 = no limit). Combined
                       with periodic checkpoints, this lets you safely
                       download in small batches across disconnects.
  --retry-unavailable  Re-queue items marked unavailable (e.g. after adding
                       cookies to 01-request/) so they are tried again.
  --addr HOST:PORT     Address for 'serve' (default 127.0.0.1:8080).
  --open               'serve' opens the browser automatically.
  -h, --help           Show this help.

EXAMPLES:
  digital-exit ingest ~/Downloads/instagram-user-20260101.zip
  # Meta's multi-zip export: pass all zips at once; they are merged into one tree.
  digital-exit ingest instagram-*.zip
  digital-exit run -root ./digital-exit --auth
  # Background runner: downloads everything, retries stalled items, survives
  # disconnects. Launch detached (see start-saved-downloads.sh in the export root):
  digital-exit fetch --watch
  digital-exit fetch --saved-limit 300 # capped manual batch
  digital-exit run --saved-limit 300 # also works, but runs the full pipeline
  digital-exit serve --open          # localhost web UI: dashboard + file manager
  # Private/auth-walled posts: log into Instagram in a browser, export cookies.txt
  # to 01-request/cookies.txt (or echo firefox > 01-request/cookies-browser.txt), then:
  digital-exit fetch --watch --retry-unavailable
`

// layout holds the resolved on-disk locations of every pipeline stage and output.
type layout struct {
	Root           string
	Request        string // 01-request
	Archive        string // 02-archive
	Media          string // 03-media
	Processed      string // 04-processed
	Output         string // 05-output
	Normalized     string // 04-processed/data.json
	DB             string // 04-processed/contacts.db (reserved for a future SQLite target)
	SavedMedia     string // 03-media/saved
	MessagesMedia  string // 03-media/messages (aggregated message gallery files)
	Unavailable    string // 03-media/unavailable.log
	MessageGallery string // 05-output/messages-gallery.html
}

func newLayout(root string) layout {
	root = filepath.Clean(root)
	return layout{
		Root:           root,
		Request:        filepath.Join(root, "01-request"),
		Archive:        filepath.Join(root, "02-archive"),
		Media:          filepath.Join(root, "03-media"),
		Processed:      filepath.Join(root, "04-processed"),
		Output:         filepath.Join(root, "05-output"),
		Normalized:     filepath.Join(root, "04-processed", "data.json"),
		DB:             filepath.Join(root, "04-processed", "contacts.db"),
		SavedMedia:     filepath.Join(root, "03-media", "saved"),
		MessagesMedia:  filepath.Join(root, "03-media", "messages"),
		Unavailable:    filepath.Join(root, "03-media", "unavailable.log"),
		MessageGallery: filepath.Join(root, "05-output", "messages-gallery.html"),
	}
}

func ensureDirs(l layout) error {
	dirs := []string{l.Request, l.Archive, l.Media, l.SavedMedia, l.MessagesMedia, l.Processed, l.Output}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

func parseRoot(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "-root" || args[i] == "--root" {
			if i+1 < len(args) {
			root := args[i+1]
			return root, append(args[:i], args[i+2:]...)
			}
			return ".", args[:i]
		}
		if len(args[i]) > 6 && args[i][:6] == "-root=" {
			root := args[i][6:]
			return root, append(args[:i], args[i+1:]...)
		}
	}
	return ".", args
}

// parseStringFlag scans args for name or alt and returns the following string
// value. Returns def if the flag is absent.
func parseStringFlag(args []string, name, alt, def string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name || args[i] == alt {
			if i+1 < len(args) {
				return args[i+1]
			}
			return def
		}
		if len(args[i]) > len(name)+1 && args[i][:len(name)+1] == name+"=" {
			return args[i][len(name)+1:]
		}
	}
	return def
}

// parseIntFlag scans args for name or alt and returns the following integer value.
// Returns 0 if the flag is absent or the value cannot be parsed.
func parseIntFlag(args []string, name, alt string) int {
	for i := 0; i < len(args); i++ {
		if args[i] == name || args[i] == alt {
			if i+1 < len(args) {
				var n int
				_, _ = fmt.Sscanf(args[i+1], "%d", &n)
				return n
			}
			return 0
		}
		if len(args[i]) > len(name)+1 && args[i][:len(name)+1] == name+"=" {
			var n int
			_, _ = fmt.Sscanf(args[i][len(name)+1:], "%d", &n)
			return n
		}
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(0)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	root, args := parseRoot(args)
	l := newLayout(root)

	hasFlag := func(name string) bool {
		for _, a := range args {
			if a == name {
				return true
			}
		}
		return false
	}

	var err error
	switch cmd {
	case "ingest":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "error: ingest requires at least one archive path (zip or folder)")
			os.Exit(2)
		}
		err = cmdIngest(args, l)
	case "run":
		if err := ensureDirs(l); err != nil {
			fatal(err)
		}

		savedLimit := parseIntFlag(args, "--saved-limit", "-saved-limit")
		err = cmdRun(l, hasFlag("--auth"), savedLimit)
	case "fetch":
		if err := ensureDirs(l); err != nil {
			fatal(err)
		}

		savedLimit := parseIntFlag(args, "--saved-limit", "-saved-limit")
		err = cmdFetch(l, savedLimit, hasFlag("--watch"), hasFlag("--retry-unavailable"))
	case "verify":
		err = cmdVerify(l)
	case "gallery":
		err = cmdMessageGallery(l)
	case "view":
		err = cmdView(l)
	case "serve":
		addr := parseStringFlag(args, "--addr", "-addr", "127.0.0.1:8080")
		err = cmdServe(l, addr, hasFlag("--open"))
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Print(usage)
		os.Exit(2)
	}

	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
