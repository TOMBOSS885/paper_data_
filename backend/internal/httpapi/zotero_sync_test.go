package httpapi

import (
	"strings"
	"testing"
)

func TestSyncTokenPrefixFitsDatabaseColumn(t *testing.T) {
	token := "pkb_zot_abcdefghijkl.abcdefghijklmnopqrstuvwxyz012345"
	prefix := syncTokenPrefix(token)
	if len(prefix) != syncTokenPrefixMaxLength {
		t.Fatalf("prefix length = %d, want %d", len(prefix), syncTokenPrefixMaxLength)
	}
	if !strings.HasPrefix(token, prefix) {
		t.Fatalf("prefix %q is not part of token %q", prefix, token)
	}
}

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
