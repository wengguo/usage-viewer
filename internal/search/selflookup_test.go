package search

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateCredentialTrimsAndAccepts(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain key", input: "sk-test-key-abc123", want: "sk-test-key-abc123"},
		{name: "surrounding whitespace", input: "  demo-key-alpha  ", want: "demo-key-alpha"},
		{name: "utf8 name", input: "密钥别名", want: "密钥别名"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateCredential(tt.input)
			if err != nil || got != tt.want {
				t.Fatalf("ValidateCredential(%q) = %q, %v, want %q, nil", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestValidateCredentialRejectsEmptyAndOversizedInput(t *testing.T) {
	longInput := strings.Repeat("a", MaximumTextRunes+1)
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "whitespace only", input: "   "},
		{name: "single char", input: "a"},
		{name: "too long", input: longInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ValidateCredential(tt.input); !errors.Is(err, ErrInvalidCredential) {
				t.Fatalf("ValidateCredential(%q) error = %v, want ErrInvalidCredential", tt.input, err)
			}
		})
	}
}
