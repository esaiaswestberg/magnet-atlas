package store

import (
	"strings"
	"unicode"
)

func rewriteSearchQuery(query string) string {
	tokens := normalizeSearchTokens(query)
	if len(tokens) == 0 {
		return ""
	}

	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if isSeasonToken(token) {
			out = append(out, token+"*")
			continue
		}
		out = append(out, token)
	}
	return strings.Join(out, " ")
}

func rewriteTSQuery(query string) string {
	tokens := normalizeSearchTokens(query)
	if len(tokens) == 0 {
		return ""
	}

	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if isSeasonToken(token) {
			out = append(out, token+":*")
			continue
		}
		out = append(out, token)
	}
	return strings.Join(out, " & ")
}

func normalizeSearchTokens(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	tokens := make([]string, 0, 8)
	var b strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			if b.Len() > 0 {
				tokens = append(tokens, b.String())
				b.Reset()
			}
		}
	}
	if b.Len() > 0 {
		tokens = append(tokens, b.String())
	}
	return tokens
}

func isSeasonToken(token string) bool {
	if len(token) < 2 || token[0] != 's' {
		return false
	}
	digits := token[1:]
	if len(digits) == 0 || len(digits) > 2 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
