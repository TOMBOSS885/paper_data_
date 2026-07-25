package db

import "testing"

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
