package pdfmeta

import (
	"strings"
	"testing"
)

func TestExtractBasic(t *testing.T) {
	pdf := []byte(`%PDF-1.4
1 0 obj
<</Type /Catalog /Pages 2 0 R>>
endobj
3 0 obj
<</Title (Attention Is All You Need) /Author (Ashish Vaswani; Noam Shazeer; Niki Parmar) /Subject (Transformer) /Keywords (deep learning; NLP) /CreationDate (D:20170612120000+00'00') >>
endobj
xref
trailer
<</Info 3 0 R>>
%%EOF`)
	m := Extract(pdf)
	if m.Title != "Attention Is All You Need" {
		t.Errorf("Title = %q, want %q", m.Title, "Attention Is All You Need")
	}
	if len(m.Authors) != 3 {
		t.Errorf("Authors len = %d, want 3 (got %v)", len(m.Authors), m.Authors)
	}
	if m.Authors[0] != "Ashish Vaswani" {
		t.Errorf("Authors[0] = %q, want Ashish Vaswani", m.Authors[0])
	}
	if m.Year != 2017 {
		t.Errorf("Year = %d, want 2017", m.Year)
	}
	if len(m.Keywords) != 2 {
		t.Errorf("Keywords len = %d, want 2 (got %v)", len(m.Keywords), m.Keywords)
	}
}

func TestExtractUTF16(t *testing.T) {
	// "你好" 的 UTF-16BE hex: 4F60 597D
	pdf := []byte(`%PDF-1.4
3 0 obj
<</Title <FEFF4F60597D> /CreationDate (D:20240101)>>
endobj
xref
%%EOF`)
	m := Extract(pdf)
	if m.Title != "你好" {
		t.Errorf("Title = %q, want 你好", m.Title)
	}
	if m.Year != 2024 {
		t.Errorf("Year = %d, want 2024", m.Year)
	}
}

func TestExtractEmpty(t *testing.T) {
	m := Extract(nil)
	if m.Title != "" || len(m.Authors) != 0 || m.Year != 0 {
		t.Errorf("expected zero value, got %+v", m)
	}
}

func TestExtractMissing(t *testing.T) {
	pdf := []byte(`%PDF-1.4
no info dict here
%%EOF`)
	m := Extract(pdf)
	if m.Title != "" || len(m.Authors) != 0 || m.Year != 0 {
		t.Errorf("expected zero value, got %+v", m)
	}
}

func TestExtractEscapedLiteral(t *testing.T) {
	// /Title (A\=\(B\)) should decode to A=(B)
	pdf := []byte(`%PDF-1.4
3 0 obj<</Title (A\=\(B\)) /Author (Doe\, J.)>>
endobj
trailer`)
	m := Extract(pdf)
	if m.Title != "A=(B)" {
		t.Errorf("Title = %q, want A=(B)", m.Title)
	}
	if len(m.Authors) != 1 || m.Authors[0] != "Doe, J." {
		t.Errorf("Authors = %v, want [Doe, J.]", m.Authors)
	}
}

func TestExtractAuthorCommaSplit(t *testing.T) {
	pdf := []byte(`%PDF-1.4
3 0 obj<</Author (Smith, John; Doe, Jane)>>
endobj`)
	m := Extract(pdf)
	if strings.Join(m.Authors, "|") != "Smith, John|Doe, Jane" {
		t.Errorf("Authors = %v", m.Authors)
	}
}

func TestExtractInvalidYear(t *testing.T) {
	pdf := []byte(`%PDF-1.4
3 0 obj<</CreationDate (D:99)>>
endobj`)
	m := Extract(pdf)
	if m.Year != 0 {
		t.Errorf("Year = %d, want 0 for invalid date", m.Year)
	}
}