package store

import "testing"

func TestRewriteSearchQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "season token", query: "The Rookie S08", want: "the rookie s08*"},
		{name: "single season token", query: "S8", want: "s8*"},
		{name: "plain query", query: "The Rookie", want: "the rookie"},
		{name: "empty", query: "   ", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteSearchQuery(tc.query); got != tc.want {
				t.Fatalf("rewriteSearchQuery(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

func TestRewriteTSQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "season token", query: "The Rookie S08", want: "the & rookie & s08:*"},
		{name: "single season token", query: "S8", want: "s8:*"},
		{name: "plain query", query: "The Rookie", want: "the & rookie"},
		{name: "empty", query: "   ", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteTSQuery(tc.query); got != tc.want {
				t.Fatalf("rewriteTSQuery(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
