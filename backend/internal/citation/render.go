package citation

import (
	"fmt"
	"strings"
)

// PaperInfo represents the paper data available for citation formatting
type PaperInfo struct {
	Title   string
	Authors []string
	Year    int
	DOI     string
	Journal string
}

// RenderCtx contains context for rendering
type RenderCtx struct {
	Paper PaperInfo
}

// renderAuthorsAPA renders authors in APA style.
// "Smith, J.; Doe, J."
func renderAuthorsAPA(authors []string) string {
	if len(authors) == 0 {
		return ""
	}
	var formatted []string
	for _, a := range authors {
		parts := strings.SplitN(a, ",", 2)
		if len(parts) == 2 {
			last := strings.TrimSpace(parts[0])
			first := strings.TrimSpace(parts[1])
			if len(first) > 0 {
				formatted = append(formatted, fmt.Sprintf("%s, %c.", last, first[0]))
			} else {
				formatted = append(formatted, last)
			}
		} else {
			// fallback
			formatted = append(formatted, strings.TrimSpace(a))
		}
	}
	if len(formatted) > 3 {
		return formatted[0] + " et al."
	}
	return strings.Join(formatted, "; ")
}

// firstAuthorLast extracts the last name of the first author.
func firstAuthorLast(authors []string) string {
	if len(authors) == 0 {
		return ""
	}
	parts := strings.SplitN(authors[0], ",", 2)
	return strings.TrimSpace(parts[0])
}

// firstAuthor extracts the full name of the first author.
func firstAuthor(authors []string) string {
	if len(authors) == 0 {
		return ""
	}
	return strings.TrimSpace(authors[0])
}

// Format renders a citation string based on a template.
// Template syntax: literal + {var}, no control flow.
// Unknown variables are preserved.
func Format(tmpl string, p PaperInfo) (string, error) {
	if len(tmpl) > 5000 {
		return "", fmt.Errorf("template too long")
	}
	if strings.Contains(tmpl, "{{") || strings.Contains(tmpl, "}}") {
		return "", fmt.Errorf("nested template braces not allowed")
	}

	res := tmpl

	// Provide variables
	yearStr := ""
	if p.Year > 0 {
		yearStr = fmt.Sprintf("%d", p.Year)
	}
	authorCountStr := fmt.Sprintf("%d", len(p.Authors))

	replacements := map[string]string{
		"{authors}":         renderAuthorsAPA(p.Authors),
		"{authorCount}":     authorCountStr,
		"{title}":           p.Title,
		"{journal}":         p.Journal,
		"{year}":            yearStr,
		"{doi}":             p.DOI,
		"{firstAuthor}":     firstAuthor(p.Authors),
		"{firstAuthorLast}": firstAuthorLast(p.Authors),
	}

	for k, v := range replacements {
		res = strings.ReplaceAll(res, k, v)
	}

	// handle escaped newlines
	res = strings.ReplaceAll(res, "\\n", "\n")

	return res, nil
}
