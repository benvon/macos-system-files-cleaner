//go:build darwin

package main

import "testing"

func TestQuotePathEscapesTerminalControlCharacters(t *testing.T) {
	t.Parallel()
	got := quotePath("safe\n\x1b[31munsafe")
	want := `"safe\n\x1b[31munsafe"`
	if got != want {
		t.Fatalf("quotePath() = %q, want %q", got, want)
	}
}

func TestTerminalErrorEscapingPrimitive(t *testing.T) {
	t.Parallel()
	got := terminalSafe("unsafe\n\x1b[31m")
	want := `unsafe\n\x1b[31m`
	if got != want {
		t.Fatalf("escaped message = %q, want %q", got, want)
	}
}
