package search

import "testing"

func TestMaskKeyKeepsPrefixAndSuffixForLongKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "typical sk key", key: "sk-test-key-abc123", want: "sk-tes***abc123"},
		{name: "exactly 13 chars", key: "abcdefghijklm", want: "abcdef***hijklm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskKey(tt.key); got != tt.want {
				t.Fatalf("MaskKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestMaskKeyFullyMasksShortKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "one char", key: "a"},
		{name: "exactly 12 chars", key: "abcdefghijkl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskKey(tt.key); got != "***" {
				t.Fatalf("MaskKey(%q) = %q, want %q", tt.key, got, "***")
			}
		})
	}
}
