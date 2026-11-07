package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// cmdRun orchestrates the full pipeline: load the normalized dataset, fetch
// saved-post media and optionally enrich follower bios via the authenticated
// API, classify every contact into the user's taxonomy, persist the result and
// emit the offline viewer. Add --auth to enable the browser/API layer.
// savedLimit, if > 0, caps the number of *new* saved-post downloads attempted
// in this run (enables safe batching across device disconnects).
func cmdRun(l layout, auth bool, savedLimit int) error {
	if err := ensureDirs(l); err != nil {
		return err
	}
	ds, err := loadDataset(l.Normalized)
	if err != nil {
		return err
	}
	if ds.Version > datasetVersion {
		fmt.Printf("warning: dataset version %d is newer than tool's %d; some fields may be missing\n", ds.Version, datasetVersion)
	}

	fmt.Println("== fetching saved-post media ==")
	if err := fetchSavedPosts(ds, l, savedLimit, nil); err != nil {
		return fmt.Errorf("fetch saved media: %w", err)
	}

	fmt.Println("== aggregating message media ==")
	aggN, err := aggregateMessageMedia(ds, l)
	if err != nil {
		return fmt.Errorf("aggregate message media: %w", err)
	}

	if auth {
		fmt.Println("== enriching follower bios (authenticated API) ==")
		enrichFollowers(ds.Contacts, l)
	} else {
		fmt.Println("== skipping bio enrichment (pass --auth with a token to enable) ==")
	}

	fmt.Println("== classifying contacts ==")
	for i := range ds.Contacts {
		ds.Contacts[i].Category = classifyContact(&ds.Contacts[i])
	}
	ds.GeneratedAt = nowRFC3339()
	if err := ds.save(l.Normalized); err != nil {
		return err
	}

	fmt.Println("== emitting viewer ==")
	if err := emitViewer(ds, l); err != nil {
		return fmt.Errorf("emit viewer: %w", err)
	}

	fmt.Println("== emitting message gallery ==")
	if err := emitMessagesGallery(ds, l); err != nil {
		return fmt.Errorf("emit message gallery: %w", err)
	}

	var company, infl, media, friend, unclassified int
	for _, c := range ds.Contacts {
		switch c.Category {
		case CatCompany:
			company++
		case CatInfluencer:
			infl++
		case CatMedia:
			media++
		case CatFriend:
			friend++
		default:
			unclassified++
		}
	}
	okSaved, unavailSaved := savedStatusCounts(ds.Saved)

	fmt.Printf("\n%s pipeline complete\n", toolName)
	fmt.Printf("  contacts: %d (company=%d influencer=%d media=%d friend=%d unclassified=%d)\n",
		len(ds.Contacts), company, infl, media, friend, unclassified)
	fmt.Printf("  posts:    %d\n", len(ds.Posts))
	fmt.Printf("  saved:    %d (downloaded=%d unavailable=%d)\n", len(ds.Saved), okSaved, unavailSaved)
	fmt.Printf("  messages: %d aggregated into gallery (%d files on disk)\n", len(ds.Messages), aggN)
	fmt.Printf("  viewer:   %s\n", relPath(l.Root, filepath.Join(l.Output, "index.html")))
	fmt.Printf("  gallery:  %s\n", relPath(l.Root, filepath.Join(l.Output, "messages-gallery.html")))
	fmt.Println("\nNext: review with 'digital-exit view', then see runbook.md before deleting anything.")
	return nil
}

// classifyContact assigns a Category using follower-count and bio heuristics.
// These are best-effort signals, not ground truth — the user should confirm.
func classifyContact(c *Contact) Category {
	bio := strings.ToLower(strings.TrimSpace(c.Bio))
	hasExternalLink := c.Raw != nil && asString(c.Raw["external_url"]) != ""
	fc := c.FollowerCount

	switch {
	case isMediaBio(bio):
		return CatMedia
	case isInfluencerBio(bio):
		return CatInfluencer
	case hasExternalLink && (isCompanyBio(bio) || fc >= 1000):
		return CatCompany
	case isJournalistBio(bio):
		return CatMedia
	case fc >= 50_000:
		return CatInfluencer
	case fc > 0 && fc < 2_000 && !hasExternalLink && bio == "":
		return CatFriend
	default:
		return CatUnclassified
	}
}

var (
	influencerTerms = []string{
		"content creator", "influencer", "youTuber", "blogger", "photographer",
		"creator on youtube", "digital creator", "artist", "model", "musician",
		"entrepreneur", "coach", "trainer", "designer",
	}
	companyTerms = []string{
		"inc.", "llc", "ltd", "corp", ".com", "official", "shop", "store",
		"business", "brand", "studio", "agency", "company", "hq@", "headquarters",
		"linktr.ee", "we are", "our team",
	}
	mediaTerms = []string{
		"journalist", "reporter", "correspondent", "news", "editor", "publisher",
		"broadcaster", "media", "press", "anchor", "columnist",
	}
	journalistTerms = []string{"journalist", "reporter", "correspondent", "columnist"}
)

func isInfluencerBio(bio string) bool { return containsAny(bio, influencerTerms) }
func isCompanyBio(bio string) bool    { return containsAny(bio, companyTerms) }
func isMediaBio(bio string) bool      { return containsAny(bio, mediaTerms) }
func isJournalistBio(bio string) bool { return containsAny(bio, journalistTerms) }

func containsAny(haystack string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(haystack, t) {
			return true
		}
	}
	return false
}

// savedStatusCounts tallies ok vs unavailable saved posts.
func savedStatusCounts(saved []SavedPost) (ok, unavail int) {
	for _, s := range saved {
		switch s.Status {
		case "ok":
			ok++
		case "unavailable":
			unavail++
		}
	}
	return
}
