package store

import (
	"encoding/json"
	"strings"
	"unicode"
)

func normalizeSearchText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(value))
	lastSpace := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
			b.WriteRune(unicode.ToLower(r))
			lastSpace = false
		default:
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func joinSearchFragments(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if normalized := normalizeSearchText(value); normalized != "" {
			parts = append(parts, normalized)
		}
	}
	return strings.Join(parts, " ")
}

func buildSearchText(title, category string, extraText []string) string {
	parts := []string{title, category}
	parts = append(parts, extraText...)
	return joinSearchFragments(parts...)
}

func normalizeTitle(title string) string {
	return joinSearchFragments(title)
}

func encodeTextList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	compact := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			compact = append(compact, value)
		}
	}
	if len(compact) == 0 {
		return "[]"
	}
	b, err := json.Marshal(compact)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeTextList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
