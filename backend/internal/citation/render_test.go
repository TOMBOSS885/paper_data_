package citation

import (
	"testing"
)

func TestFormatAPA(t *testing.T) {
	p := PaperInfo{
		Title:   "Attention Is All You Need",
		Authors: []string{"Vaswani, Ashish", "Shazeer, Noam", "Parmar, Niki", "Uszkoreit, Jakob"},
		Year:    2017,
		Journal: "NIPS",
	}
	tmpl := "{authors} ({year}). {title}. *{journal}*."
	res, err := Format(tmpl, p)
	if err != nil {
		t.Fatal(err)
	}
	expected := "Vaswani, A. et al. (2017). Attention Is All You Need. *NIPS*."
	if res != expected {
		t.Errorf("expected %q, got %q", expected, res)
	}
}
