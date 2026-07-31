package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestWithNoStoreHeaders(t *testing.T) {
	handler := withNoStoreHeaders(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, "/api/tags", nil))

	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("Vary"); got != "Accept-Encoding, Cookie" {
		t.Fatalf("Vary = %q", got)
	}
}

func TestCORSPreflightAllowsPut(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/papers/1/tags", nil)

	server.withMiddleware(http.NotFoundHandler()).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
}
