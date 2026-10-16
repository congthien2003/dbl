package grid

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDisplayDistinguishesNullFromEmpty(t *testing.T) {
	null := Value{Kind: KindNull}
	empty := Value{Kind: KindText}
	if got := null.Display(20); got != "NULL" {
		t.Errorf("null displays %q, want NULL", got)
	}
	if got := empty.Display(20); got != "(empty)" {
		t.Errorf("empty displays %q, want (empty)", got)
	}
	if null.Equal(empty) {
		t.Error("NULL equals the empty string")
	}
}

func TestDisplayTruncatesOnlyForDisplay(t *testing.T) {
	full := strings.Repeat("abcdefghij", 100)
	v := Value{Kind: KindText, Text: full}
	got := v.Display(50)
	if n := utf8.RuneCountInString(got); n > 50 {
		t.Errorf("display has %d runes, want <= 50", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("display %q does not end with an ellipsis", got)
	}
	if v.CopyText() != full {
		t.Error("CopyText is not the full original value")
	}
	if v.Text != full {
		t.Error("Display changed the value")
	}
}

func TestDisplayMultilineAndUnicode(t *testing.T) {
	if got := (Value{Kind: KindText, Text: "a\nb\tc"}).Display(20); got != "a b c" {
		t.Errorf("multiline displays %q, want %q", got, "a b c")
	}
	v := Value{Kind: KindText, Text: "Việt Nam 🙂 Việt Nam 🙂"}
	got := v.Display(10)
	if !utf8.ValidString(got) {
		t.Errorf("display %q is not valid UTF-8", got)
	}
	if n := utf8.RuneCountInString(got); n > 10 {
		t.Errorf("display has %d runes, want <= 10", n)
	}
}

func TestBinaryDisplayIsPlaceholder(t *testing.T) {
	v := Value{Kind: KindBinary, Size: 2048}
	const want = "<binary 2048 bytes>"
	if got := v.Display(10); got != want {
		t.Errorf("Display = %q, want %q", got, want)
	}
	if got := v.CopyText(); got != want {
		t.Errorf("CopyText = %q, want %q", got, want)
	}
}

func TestNullCopiesAsEmpty(t *testing.T) {
	if got := (Value{Kind: KindNull}).CopyText(); got != "" {
		t.Errorf("CopyText = %q, want empty", got)
	}
}
