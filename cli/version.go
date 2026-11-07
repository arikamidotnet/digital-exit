package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// datasetVersion is bumped when the normalized schema changes. The viewer ignores
// unknown higher versions but logs a warning.
const datasetVersion = 1

const toolName = "digital-exit"

// ensureParentFile makes sure the parent directory of path exists.
func ensureParentFile(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

// appendLog appends a line to logPath, creating it if needed.
func appendLog(logPath, line string) error {
	if err := ensureParentFile(logPath); err != nil {
		return err
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, err = f.WriteString(line)
	return err
}

// sanitizeFilename turns an arbitrary string into a safe filesystem component.
func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, string(os.PathSeparator), "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if s == "" {
		return "unnamed"
	}
	return s
}

// relPath reports p relative to base, falling back to p unchanged.
func relPath(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return r
	}
	return p
}

// humanCount formats large integers with k/M suffixes for log output.
func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
