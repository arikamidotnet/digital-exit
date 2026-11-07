package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Kind identifies whether a contact is someone following the account or someone
// the account follows.
type Kind string

const (
	KindFollower  Kind = "follower"
	KindFollowing Kind = "following"
)

// Category is the user's taxonomy for classifying each contact.
type Category string

const (
	CatCompany      Category = "Company"
	CatInfluencer   Category = "Influencer"
	CatFriend       Category = "Friend"
	CatMedia        Category = "Media"
	CatUnclassified Category = "Unclassified"
)

// Contact is a normalized follower or following entry.
type Contact struct {
	Username       string         `json:"username"`
	DisplayName    string         `json:"displayName"`
	Kind           Kind           `json:"kind"`
	Category       Category       `json:"category"`
	Bio            string         `json:"bio,omitempty"`
	URL            string         `json:"url"`
	FollowerCount  int            `json:"followerCount"`
	FollowingCount int            `json:"followingCount"`
	Verified       bool           `json:"verified"`
	Raw            map[string]any `json:"raw,omitempty"`
}

// PostRecord is a normalized post/story/reel pulled from the archive media.
type PostRecord struct {
	Shortcode string   `json:"shortcode"`
	URL       string   `json:"url"`
	Type      string   `json:"type"` // post | story | reel | archived_post
	Caption   string   `json:"caption,omitempty"`
	CreatedAt string   `json:"createdAt"`
	Media     []string `json:"media"` // relative paths inside 03-media or the archive
	Geotag    string   `json:"geotag,omitempty"`
	Tagged    []string `json:"tagged,omitempty"`
}

// SavedPost is a saved post. LocalPath is filled in by media.go once downloaded;
// Status is ok | unavailable after the fetch stage.
type SavedPost struct {
	URL       string `json:"url"`
	Author    string `json:"author,omitempty"`
	SavedAt   string `json:"savedAt,omitempty"`
	Kind      string `json:"kind"` // post | reel
	LocalPath string `json:"localPath,omitempty"`
	Status    string `json:"status"` // pending | ok | unavailable
	Retries   int    `json:"retries"`
}

// MessageMedia is a single media item pulled from a DM conversation: the user
// who sent it, the date it was sent, its kind (photo/video/audio), the
// conversation title, and where it lives — SourcePath inside the export, or,
// once aggregated, the renamed LocalPath under 03-media/messages.
type MessageMedia struct {
	User       string `json:"user"`
	Date       string `json:"date"` // YYYY-MM-DD
	Type       string `json:"type"` // photo | video | audio
	Title      string `json:"title,omitempty"`
	SourcePath string `json:"sourcePath"`
	LocalPath  string `json:"localPath,omitempty"`
}

// Dataset is the normalized, persisted output consumed by the viewer.
type Dataset struct {
	Version     int            `json:"version"`
	AccountName string         `json:"accountName,omitempty"`
	GeneratedAt string         `json:"generatedAt"`
	Posts       []PostRecord   `json:"posts"`
	Contacts    []Contact      `json:"contacts"`
	Saved       []SavedPost    `json:"saved"`
	Messages    []MessageMedia `json:"messages"`
}

func (d *Dataset) save(path string) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".dataset-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func loadDataset(path string) (*Dataset, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Dataset{Version: datasetVersion}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var d Dataset
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &d, nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
