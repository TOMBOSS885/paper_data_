package httpapi

import "testing"

func TestSyncMetadataHashIgnoresTagOrder(t *testing.T) {
	base := syncMetadata{
		ItemType: "journalArticle",
		Title:    "A paper",
		Authors:  []string{"Ada Lovelace"},
		Tags:     []string{"zotero", "同步"},
	}
	reordered := base
	reordered.Tags = []string{"同步", "zotero"}
	if syncMetadataHash(base) != syncMetadataHash(reordered) {
		t.Fatal("tag ordering must not cause a sync metadata conflict")
	}
}
