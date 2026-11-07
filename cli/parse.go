package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// fixMojibake reverses the common Instagram export bug where UTF-8 bytes were
// written out as if they were Latin-1 codepoints. If a caption was double-encoded
// this restores the original text; otherwise it returns the input unchanged.
func fixMojibake(s string) string {
	hasHigh := false
	for _, r := range s {
		if r > 0x7F {
			hasHigh = true
			break
		}
	}
	if !hasHigh {
		return s
	}
	var raw []byte
	for _, r := range s {
		if r <= 0xFF {
			raw = append(raw, byte(r))
		} else {
			var b [4]byte
			n := utf8.EncodeRune(b[:], r)
			raw = append(raw, b[:n]...)
		}
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	return s
}

var (
	igPostRe    = regexp.MustCompile(`instagram\.com/(?:[^/]+/)?(p|reel|tv)/([A-Za-z0-9_-]+)`)
	igProfileRe = regexp.MustCompile(`instagram\.com/([^/?#]+)/`)
	handleRe    = regexp.MustCompile(`@([A-Za-z0-9_]+)`)
)

// postKindFromURL classifies an Instagram permalink as post, reel or tv.
func postKindFromURL(u string) string {
	m := igPostRe.FindStringSubmatch(u)
	if len(m) == 3 {
		switch m[1] {
		case "reel":
			return "reel"
		case "tv":
			return "tv"
		default:
			return "post"
		}
	}
	return "post"
}

// shortcodeFromURL returns the post shortcode, if any.
func shortcodeFromURL(u string) string {
	m := igPostRe.FindStringSubmatch(u)
	if len(m) == 3 {
		return m[2]
	}
	return ""
}

// firstInstagramURL scans a flat set of strings for the first Instagram permalink.
func firstInstagramURL(ss []string) string {
	for _, s := range ss {
		if strings.Contains(s, "instagram.com/") && (strings.Contains(s, "/p/") || strings.Contains(s, "/reel/") || strings.Contains(s, "/tv/")) {
			return s
		}
	}
	return ""
}

// firstHandle extracts the first @handle found in any of the given strings.
func firstHandle(ss ...string) string {
	for _, s := range ss {
		if m := handleRe.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

// linkFromNested pulls an Instagram permalink out of a record's label map,
// tolerating both the string_map_data and label_values shapes and any localized
// label key. It returns url, author, kind and any embedded timestamp.
func linkFromNested(rec map[string]any) (string, string, string, string) {
	// Saved posts store the permalink under a nested "post" object.
	if post, ok := rec["post"].(map[string]any); ok {
		if u := firstInstagramURL([]string{asString(post["url"]), asString(post["permalink"])}); u != "" {
			return normalizeIGLink(u), "", postKindFromURL(u), asString(post["taken_at"])
		}
	}
	for _, key := range []string{"string_map_data", "label_values"} {
		// Meta exports label_values / string_map_data either as a map (older shape)
		// or an array of {label,value,href} entries (current shape). Normalize both.
		var entries []map[string]any
		switch t := rec[key].(type) {
		case map[string]any:
			for _, rawVal := range t {
				if m, ok := rawVal.(map[string]any); ok {
					entries = append(entries, m)
				}
			}
		case []any:
			for _, e := range t {
				if m, ok := e.(map[string]any); ok {
					entries = append(entries, m)
				}
			}
		}
		for _, valMap := range entries {
			if href, ok := valMap["href"].(string); ok && strings.Contains(href, "instagram.com/") {
				return normalizeIGLink(href), authorFromValue(valMap), postKindFromURL(href), asString(valMap["timestamp"])
			}
			if v, ok := valMap["value"].(string); ok && strings.Contains(v, "instagram.com/") && (strings.Contains(v, "/p/") || strings.Contains(v, "/reel/")) {
				return normalizeIGLink(v), authorFromValue(valMap), postKindFromURL(v), asString(valMap["timestamp"])
			}
		}
	}
	for _, rawVal := range rec {
		if s, ok := rawVal.(string); ok && strings.Contains(s, "instagram.com/") && (strings.Contains(s, "/p/") || strings.Contains(s, "/reel/")) {
			return normalizeIGLink(s), firstHandle(s), postKindFromURL(s), ""
		}
	}
	return "", "", "", ""
}

func authorFromValue(valMap map[string]any) string {
	for _, v := range valMap {
		if s, ok := v.(string); ok {
			if h := firstHandle(s); h != "" && !strings.HasPrefix(h, "@") {
				return h
			}
		}
	}
	return ""
}

// normalizeIGLink trims query/fragment and a trailing slash from an IG permalink.
func normalizeIGLink(u string) string {
	u = strings.TrimRight(u, "/")
	if u, err := url.Parse(u); err == nil {
		u.RawQuery, u.Fragment = "", ""
		return u.String()
	}
	return u
}

// extractSavedPosts reads saved_posts.json (either shape) into SavedPost records.
func extractSavedPosts(path string) []SavedPost {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}

	var records []map[string]any
	switch typed := root.(type) {
	case []any: // bare array shape
		for _, e := range typed {
			if m, ok := e.(map[string]any); ok {
				records = append(records, m)
			}
		}
	case map[string]any: // { "saved_saved_media": [ ... ] } and friends
		for _, v := range typed {
			if arr, ok := v.([]any); ok {
				for _, e := range arr {
					if m, ok := e.(map[string]any); ok {
						records = append(records, m)
					}
				}
			}
		}
	}

	out := make([]SavedPost, 0, len(records))
	for _, r := range records {
		link, author, kind, ts := linkFromNested(r)
		if link == "" {
			continue
		}
		if kind == "" {
			kind = postKindFromURL(link)
		}
		out = append(out, SavedPost{
			URL:     link,
			Author:  author,
			Kind:    kind,
			SavedAt: ts,
			Status:  "pending",
		})
	}
	return out
}

// extractUserList pulls a list of user maps from a followers/following JSON file.
// It handles two real Meta export shapes:
//  1. {"followers":[{username,biography,...}]} or {relationships_following:[{title:...}]}
//     — objects with profile fields directly (the older / enriched shape).
//  2. [{string_list_data:[{value,href,timestamp}],...}] — the connections list
//     format, which only carries usernames; bios/counts come from Stage 3 enrichment.
//
// Layout drift is tolerated by scanning for whichever key matches first.
func extractUserList(path string) []map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}

	// Shape 1: a wrapper key holding an array of profile objects.
	for _, k := range []string{"followers", "following", "relationships_following"} {
		if m, ok := root.(map[string]any); ok {
			if arr, ok := m[k].([]any); ok {
				if users := collectUserMaps(arr); len(users) > 0 {
					return users
				}
			}
		}
	}
	// Shape 2: a bare or wrapped array of {string_list_data:[...]} entries. Walk the
	// raw tree to find any such array and hand it wholesale to collectStringListUsers.
	if users := collectStringListArray(root); len(users) > 0 {
		return users
	}
	// Last resort: anything that looks like a profile object.
	for _, v := range flatten(root) {
		if u := collectUserMaps(v); len(u) > 0 {
			return u
		}
	}
	return nil
}

// collectStringListArray walks the JSON tree and returns synthesized profiles for the
// first array whose members all carry string_list_data.
func collectStringListArray(node any) []map[string]any {
	switch t := node.(type) {
	case []any:
		if users := collectStringListUsers(t); len(users) > 0 {
			return users
		}
	case map[string]any:
		for _, v := range t {
			if u := collectStringListArray(v); len(u) > 0 {
				return u
			}
		}
	}
	return nil
}

// collectUserMaps returns elements of arr that already look like profile objects
// (they carry a username, id, or biography field).
func collectUserMaps(arr any) []map[string]any {
	list, ok := arr.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if m["username"] != nil || m["id"] != nil || m["biography"] != nil || m["bio"] != nil {
			out = append(out, m)
		}
	}
	return out
}

// collectStringListUsers handles the Meta connections-list format: each entry is
// {string_list_data:[{value,href,timestamp}]}. It synthesizes a minimal profile map
// with just "username" so downstream enrichment can fill in bio/counts.
func collectStringListUsers(list []any) []map[string]any {
	var out []map[string]any
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		sld, ok := m["string_list_data"].([]any)
		if !ok || len(sld) == 0 {
			continue
		}
		entry, ok := sld[0].(map[string]any)
		if !ok {
			continue
		}
		// Username comes from value (followers) or title (following.json), since
		// following entries often lack a clean value when the href is /_u/handle.
		username := asString(entry["value"])
		if username == "" {
			username = asString(m["title"]) // Meta's following.json uses title for the handle
		}
		// Fall back to parsing it out of the href (/u/ or /handle form).
		if username == "" {
			if u := asString(entry["href"]); strings.Contains(u, "instagram.com/") {
				parts := strings.SplitN(u, "instagram.com/", 2)
				username = parts[1]
			}
		}
		if username == "" {
			continue
		}
		out = append(out, map[string]any{
			"username":         username,
			"full_name":        asString(m["title"]), // title often holds the handle for following.json
			"biography":        "",
			"edge_followed_by": map[string]any{"count": float64(0)},
			"edge_following":   map[string]any{"count": float64(0)},
		})
	}
	return out
}

// flatten walks a nested structure and yields every map[string]any it finds.
func flatten(v any) []map[string]any {
	var out []map[string]any
	switch typed := v.(type) {
	case map[string]any:
		out = append(out, typed)
		for _, val := range typed {
			out = append(out, flatten(val)...)
		}
	case []any:
		for _, e := range typed {
			out = append(out, flatten(e)...)
		}
	}
	return out
}

// asString renders a JSON value as a string, tolerating float64 timestamps.
func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return itoa(int64(t))
	default:
		return ""
	}
}

// itoa renders an int64 without pulling in strconv for a single call.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// walkJSONFiles invokes fn on every .json file under root (recursively).
func walkJSONFiles(root string, fn func(path string)) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(p), ".json") {
			fn(p)
		}
		return nil
	})
}
