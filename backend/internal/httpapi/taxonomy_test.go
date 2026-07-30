package httpapi

import "testing"

func TestBulkTaxonomySchemaFor(t *testing.T) {
	tests := []struct {
		kind        string
		joinTable   string
		joinColumn  string
		countTable  string
		countColumn string
	}{
		{kind: "tags", joinTable: "paper_tags", joinColumn: "tag_id", countTable: "tags", countColumn: "usage_count"},
		{kind: "categories", joinTable: "paper_categories", joinColumn: "category_id", countTable: "categories", countColumn: "paper_count"},
	}

	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			schema, ok := bulkTaxonomySchemaFor(test.kind)
			if !ok {
				t.Fatalf("expected schema for %q", test.kind)
			}
			if schema.joinTable != test.joinTable || schema.joinColumn != test.joinColumn ||
				schema.countTable != test.countTable || schema.countColumn != test.countColumn {
				t.Fatalf("unexpected schema for %q: %+v", test.kind, schema)
			}
		})
	}

	if _, ok := bulkTaxonomySchemaFor("invalid"); ok {
		t.Fatal("invalid taxonomy kind was accepted")
	}
}
