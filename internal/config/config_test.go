package config

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "https unchanged", in: "https://weirdstats.com/", want: "https://weirdstats.com"},
		{name: "production host gets https", in: "weirdstats.com", want: "https://weirdstats.com"},
		{name: "localhost gets http", in: "localhost:8080", want: "http://localhost:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeBaseURL(tt.in); got != tt.want {
				t.Fatalf("normalizeBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
