package db

import (
	"strings"
	"testing"
)

func TestSplitSQLStatementsPreservesQuotedSemicolons(t *testing.T) {
	input := "CREATE TABLE a (v VARCHAR(20) DEFAULT 'a;b'); CREATE TABLE b (id INT);"
	statements := splitSQLStatements(input)
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(statements))
	}
	if statements[0] != "CREATE TABLE a (v VARCHAR(20) DEFAULT 'a;b')" {
		t.Fatalf("unexpected first statement: %q", statements[0])
	}
}

func TestTrashMigrationIsSafeForExistingSoftDeletesAndRetries(t *testing.T) {
	body, err := migrations.ReadFile("migrations/007_trash_retention.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(body)
	for _, required := range []string{
		"WHERE deleted_at IS NOT NULL",
		"p.deleted_at IS NULL",
		"information_schema.statistics",
		"idx_papers_deleted_at",
	} {
		if !strings.Contains(sqlText, required) {
			t.Errorf("migration does not contain %q", required)
		}
	}
}

func TestPaperQueryIndexMigrationRepairsCountersAndIsRetrySafe(t *testing.T) {
	body, err := migrations.ReadFile("migrations/009_paper_query_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(body)
	for _, required := range []string{
		"SET t.usage_count = COALESCE(counts.active_count, 0)",
		"SET c.paper_count = COALESCE(counts.active_count, 0)",
		"information_schema.statistics",
		"idx_papers_active_added",
		"idx_papers_active_updated",
		"idx_papers_active_published",
		"idx_papers_active_status_added",
		"idx_papers_active_favorite_added",
		"idx_papers_active_doi",
	} {
		if !strings.Contains(sqlText, required) {
			t.Errorf("migration does not contain %q", required)
		}
	}
}

func TestSessionTokenVersionMigrationIsRetrySafe(t *testing.T) {
	body, err := migrations.ReadFile("migrations/008_session_token_version.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(body)
	for _, required := range []string{
		"information_schema.columns",
		"column_name = 'token_version'",
		"ALTER TABLE sessions ADD COLUMN token_version",
	} {
		if !strings.Contains(sqlText, required) {
			t.Errorf("migration does not contain %q", required)
		}
	}
}
