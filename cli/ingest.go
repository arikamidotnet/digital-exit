package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cmdIngest accepts one or more raw export inputs — each may be a .zip, .tar.gz,
// or an already-unzipped folder. Meta's official download ships as several zips that
// together form a single tree (connections.zip, media.zip, etc.), so multiple paths
// are extracted into a shared staging dir and merged before normalization.
func cmdIngest(inputs []string, l layout) error {
	if err := ensureDirs(l); err != nil {
		return err
	}

	dest := filepath.Join(l.Archive, "export")
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	var staged []string
	for _, in := range inputs {
		src, err := filepath.Abs(in)
		if err != nil {
			return fmt.Errorf("input %s: %w", in, err)
		}
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("input %s: %w", in, err)
		}
		switch {
		case info.IsDir():
			rootDest := filepath.Join(dest, sanitizeFilename(filepath.Base(src)))
			if err := copyTree(src, rootDest); err != nil {
				return fmt.Errorf("stage %s: %w", in, err)
			}
			staged = append(staged, rootDest)
		case strings.EqualFold(filepath.Ext(src), ".zip"):
			if err := unzipInto(src, dest); err != nil {
				return fmt.Errorf("unzip %s: %w", in, err)
			}
			staged = append(staged, dest)
		case strings.EqualFold(filepath.Ext(src), ".tar.gz"), strings.EqualFold(filepath.Ext(src), ".tgz"):
			if err := untarGzInto(src, dest); err != nil {
				return fmt.Errorf("untar %s: %w", in, err)
			}
			staged = append(staged, dest)
		default:
			return fmt.Errorf("unsupported input type (expected .zip/.tar.gz or a folder): %s", in)
		}
	}

	root := findExportRoot(dest)
	_ = staged // kept for diagnostics if merge order ever matters

	det := detectLayout(root)
	ds, err := normalizeDataset(root, det)
	if err != nil {
		return err
	}
	ds.Version = datasetVersion
	ds.AccountName = accountNameFromRoot(root)
	ds.GeneratedAt = nowRFC3339()
	if err := ds.save(l.Normalized); err != nil {
		return err
	}

	fmt.Printf("ingested %s\n", relPath(l.Root, root))
	fmt.Printf("  contacts: %d (followers=%d following=%d)\n",
		len(ds.Contacts), countKind(ds.Contacts, KindFollower), countKind(ds.Contacts, KindFollowing))
	fmt.Printf("  posts:    %d\n", len(ds.Posts))
	fmt.Printf("  saved:    %d\n", len(ds.Saved))
	fmt.Printf("  normalized -> %s\n", relPath(l.Root, l.Normalized))
	return nil
}

// detectLayout reports which known Instagram export folders/files are present.
func detectLayout(root string) detected {
	var d detected
	_ = filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, "followers") && strings.HasSuffix(name, ".json"):
			d.hasFollowers = true
		case name == "following.json":
			d.hasFollowing = true
		case strings.Contains(p, "/saved/") && strings.HasSuffix(name, ".json"):
			d.hasSaved = true
		case strings.Contains(p, "/media/") && strings.HasSuffix(name, ".json"):
			d.hasMedia = true
		}
		return nil
	})
	return d
}

// findExportRoot locates the top-level export folder (the one holding connections/,
// media/ and your_instagram_activity/) inside a staged archive. Exports may be
// wrapped in extra folders or handed over as the export folder itself, so the search
// is bounded but not depth-fixed; the documented instagram-user-DATE name wins when
// several candidates match. Falls back to dir rather than failing.
func findExportRoot(dir string) string {
	if isExportRoot(dir) {
		return dir
	}
	var candidates []string
	_ = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil || !e.IsDir() {
			return nil
		}
		nested := strings.Count(strings.TrimPrefix(p, dir), string(os.PathSeparator))
		if nested > 3 {
			return filepath.SkipDir
		}
		if isExportRoot(p) {
			candidates = append(candidates, p)
		}
		return nil
	})
	for _, c := range candidates {
		if strings.HasPrefix(filepath.Base(c), "instagram-user-") {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return dir
}

// isExportRoot reports whether p is the top of an Instagram export. One marker is too
// weak — your_instagram_activity/ has its own media/ and saved/ subfolders — so require
// either connections/ alongside another top-level folder, or both media/ and
// your_instagram_activity/ as siblings (an export that omitted followers).
func isExportRoot(p string) bool {
	hasConnections := dirExists(filepath.Join(p, "connections"))
	hasMedia := dirExists(filepath.Join(p, "media"))
	hasActivity := dirExists(filepath.Join(p, "your_instagram_activity"))
	return (hasConnections && (hasMedia || hasActivity)) || (hasMedia && hasActivity)
}

// normalizeDataset builds a Dataset from an extracted export root.
func normalizeDataset(root string, det detected) (*Dataset, error) {
	ds := &Dataset{
		Posts:    []PostRecord{},
		Contacts: []Contact{},
		Saved:    []SavedPost{},
	}
	ds.Contacts = normalizeContacts(root)
	ds.Saved = normalizeSaved(root)
	ds.Messages = collectMessageMedia(root)
	if err := normalizePosts(root, ds); err != nil {
		return nil, err
	}
	return ds, nil
}

// detected is the compact layout summary returned by detectLayout.
type detected struct {
	hasFollowers, hasFollowing, hasSaved, hasMedia bool
}

// normalizeSaved discovers every saved_* list in the export and merges them. The flat
// list is a superset over collections, so permalinks are deduped; files are located by
// name rather than hard-coded path because the folder layout drifts between versions.
func normalizeSaved(root string) []SavedPost {
	var found []SavedPost
	_ = filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".json") || !strings.HasPrefix(name, "saved") {
			return nil
		}
		found = append(found, extractSavedPosts(p)...)
		return nil
	})
	if len(found) == 0 {
		return []SavedPost{}
	}
	return dedupeSaved(found)
}

// dedupeSaved collapses repeated permalinks, keeping the richest record of each.
func dedupeSaved(saved []SavedPost) []SavedPost {
	index := map[string]int{}
	out := make([]SavedPost, 0, len(saved))
	for _, s := range saved {
		if i, ok := index[s.URL]; ok {
			if out[i].Author == "" && s.Author != "" {
				out[i].Author = s.Author
			}
			if out[i].SavedAt == "" && s.SavedAt != "" {
				out[i].SavedAt = s.SavedAt
			}
			continue
		}
		index[s.URL] = len(out)
		out = append(out, s)
	}
	return out
}

// normalizePosts walks media/**/*.json and parses each post file into a record.
// If no JSON sidecars are present (Meta's newer export ships raw media files only),
// it falls back to enumerating the media files themselves so the viewer still lists
// them, even without captions/timestamps. Post JSON may be a bare object or an array
// of objects (some exports wrap each file as a single-element list).
func normalizePosts(root string, ds *Dataset) error {
	mediaRoot := filepath.Join(root, "media")
	if !dirExists(mediaRoot) {
		ds.Posts = []PostRecord{}
		return nil
	}
	var posts []PostRecord
	jsonCount := 0
	err := walkJSONFiles(mediaRoot, func(p string) {
		data, err := os.ReadFile(p)
		if err != nil {
			return
		}
		for _, m := range postMaps(data) {
			jsonCount++
			posts = append(posts, postFromMap(m))
		}
	})
	if err != nil {
		return err
	}
	// Fallback: no JSON sidecars — enumerate the raw media files as posts so they
	// still appear in the viewer and saved-post correlation has something to match.
	if jsonCount == 0 {
		posts = normalizeMediaFiles(mediaRoot, ds)
	}
	ds.Posts = posts
	return nil
}

// normalizeMediaFiles builds bare PostRecords from raw media files when no JSON
// sidecars exist. The shortcode is derived from the filename; type from the parent dir.
func normalizeMediaFiles(mediaRoot string, ds *Dataset) []PostRecord {
	var posts []PostRecord
	_ = filepath.WalkDir(mediaRoot, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !isMediaExt(ext) {
			return nil
		}
		shortcode := sanitizeFilename(strings.TrimSuffix(e.Name(), ext))
		rel, _ := filepath.Rel(mediaRoot, p)
		posts = append(posts, PostRecord{
			Shortcode: shortcode,
			URL:       "https://www.instagram.com/p/" + shortcode + "/",
			Type:      postTypeFromDir(filepath.Dir(rel)),
			Media:     []string{filepath.ToSlash(rel)},
		})
		return nil
	})
	return posts
}

// isMediaExt reports whether ext looks like an Instagram media file.
func isMediaExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".mp4", ".mov", ".webm":
		return true
	default:
		return false
	}
}

// postTypeFromDir classifies a media file by its parent directory name.
func postTypeFromDir(dir string) string {
	base := strings.ToLower(filepath.Base(dir))
	switch base {
	case "reels", "reel":
		return "reel"
	case "igtv", "tv":
		return "tv"
	case "archived_posts", "stories", "story":
		return "story"
	default:
		if strings.Contains(base, "archive") {
			return "post"
		}
		return "post"
	}
}

// normalizeContacts builds contacts from the followers and following lists.
func normalizeContacts(root string) []Contact {
	base := filepath.Join(root, "connections", "followers_and_following")
	var contacts []Contact
	if dirExists(base) {
		entries, err := os.ReadDir(base)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				kind := KindFollower
				if strings.HasPrefix(e.Name(), "following") {
					kind = KindFollowing
				} else if !strings.HasPrefix(e.Name(), "followers") {
					continue
				}
				for _, m := range extractUserList(filepath.Join(base, e.Name())) {
					contacts = append(contacts, contactFromMap(m, kind))
				}
			}
		}
	}
	return dedupeContacts(contacts)
}

func contactFromMap(m map[string]any, kind Kind) Contact {
	username := asString(m["username"])
	c := Contact{
		Username:       username,
		DisplayName:    asString(m["full_name"]),
		Kind:           kind,
		Category:       CatUnclassified,
		Bio:            fixMojibake(strings.TrimSpace(asString(m["biography"]) + " " + asString(m["bio"]))),
		URL:            profileURL(username),
		FollowerCount:  followCount(m, "edge_followed_by"),
		FollowingCount: followCount(m, "edge_following"),
		Verified:       boolVal(m["is_verified"]),
		Raw:            m,
	}
	if c.DisplayName == "" {
		c.DisplayName = username
	}
	return c
}

func dedupeContacts(contacts []Contact) []Contact {
	seen := map[string]bool{}
	out := make([]Contact, 0, len(contacts))
	for _, c := range contacts {
		key := string(c.Kind) + "|" + c.Username
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

func postFromMap(m map[string]any) PostRecord {
	sc := asString(m["shortcode"])
	p := PostRecord{
		Shortcode: sc,
		Type:      "post",
		Caption:   fixMojibake(asString(m["caption"])),
		CreatedAt: unixTime(m, "taken_at"),
	}
	if u := firstInstagramURL([]string{asString(m["permalink"]), asString(m["url"])}); u != "" {
		p.URL = u
	} else if sc != "" {
		p.URL = "https://www.instagram.com/p/" + sc + "/"
	}
	p.Type = postTypeFromMedia(p.URL, asString(m["media_type"]), asString(m["typename"]))
	return p
}

// postTypeFromMedia classifies a media file by permalink kind and metadata.
func postTypeFromMedia(u, mt, typename string) string {
	upper := strings.ToUpper(typename) + " " + strings.ToUpper(mt)
	switch {
	case strings.Contains(upper, "REEL"):
		return "reel"
	case strings.Contains(strings.ToLower(u), "/reel/"):
		return "reel"
	case strings.Contains(strings.ToLower(u), "/tv/"):
		return "tv"
	default:
		return "post"
	}
}

func accountNameFromRoot(root string) string {
	base := filepath.Base(filepath.Clean(root))
	if strings.HasPrefix(base, "instagram-user-") {
		return base
	}
	return base
}

func countKind(contacts []Contact, kind Kind) int {
	n := 0
	for _, c := range contacts {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

func profileURL(username string) string {
	if username == "" {
		return ""
	}
	return "https://www.instagram.com/" + sanitizeFilename(username) + "/"
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// copyTree recursively copies src into dst (files only).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, relPath(src, p)), 0o755)
		}
		out := filepath.Join(dst, relPath(src, p))
		if err := ensureParentFile(out); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		outF, err := os.Create(out)
		if err != nil {
			return err
		}
		defer outF.Close()
		_, err = io.Copy(outF, in)
		return err
	})
}

// unzipMerge extracts src into the shared staging dir `dest`, merging with any
// content already there. Multiple zips from Meta's multi-zip export layer onto one
// tree without clearing prior entries (unlike a single-input extract which would wipe).
func unzipInto(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		target := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := ensureParentFile(target); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		func() {
			defer rc.Close()
			out, err := os.Create(target)
			if err != nil {
				return
			}
			defer out.Close()
			io.Copy(out, rc)
		}()
	}
	return nil
}

// untarGzInto extracts a gzip-compressed tar archive into dest without clearing it
// first, so multiple archives can be layered onto one staging directory. Used by the
// multi-zip merge path; same rationale as unzipInto.
func untarGzInto(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := ensureParentFile(target); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			func() {
				defer out.Close()
				io.Copy(out, tr)
			}()
		}
	}
	return nil
}

// --- small JSON helpers (stdlib only) ---

// isPostJSON reports whether data parses as an Instagram post record.
func isPostJSON(data []byte) bool {
	for _, m := range postMaps(data) {
		_ = m
		return true
	}
	return false
}

// postMaps extracts all post-record maps from a media JSON file. The data may be a
// bare object, an array of objects, or nested under a wrapper key — this handles each.
func postMaps(data []byte) []map[string]any {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	switch t := root.(type) {
	case map[string]any:
		if hasShortcode(t) {
			return []map[string]any{t}
		}
		// Wrapped: {media:[...]} or similar — scan nested arrays.
		for _, v := range t {
			if ms := postMapsFromSlice(v); len(ms) > 0 {
				return ms
			}
		}
	case []any:
		return postMapsFromSlice(t)
	}
	return nil
}

// postMapsFromSlice returns maps from a JSON array (or array-valued field) that have
// a shortcode, tolerating non-object entries.
func postMapsFromSlice(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok || !hasShortcode(m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// hasShortcode reports whether a parsed post object looks like an Instagram record.
func hasShortcode(m map[string]any) bool {
	return m["shortcode"] != nil || m["url"] != nil || m["permalink"] != nil
}

// followCount reads a follower/following count that the export may store either
// as a bare integer or as an object like {"count": N}.
func followCount(m map[string]any, key string) int {
	switch t := m[key].(type) {
	case float64:
		return int(t)
	case map[string]any:
		if c, ok := t["count"].(float64); ok {
			return int(c)
		}
	case int:
		return t
	}
	return 0
}

func intNum(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}

func boolVal(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

// unixTime converts a taken_at value (unix seconds as float64 or numeric string)
// to an RFC3339 timestamp.
func unixTime(m map[string]any, key string) string {
	switch t := m[key].(type) {
	case float64:
		if t == 0 {
			return ""
		}
		return time.Unix(int64(t), 0).UTC().Format(time.RFC3339)
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		if err == nil && n != 0 {
			return time.Unix(n, 0).UTC().Format(time.RFC3339)
		}
		return t
	default:
		return ""
	}
}
