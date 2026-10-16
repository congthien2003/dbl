// Package grid holds typed cell values, their display rules and local drafts.
package grid

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is the type of a cell value. The grid shows each kind distinctly.
type Kind uint8

// Value kinds.
const (
	KindNull Kind = iota
	KindText
	KindNumber
	KindBool
	KindDateTime
	KindJSON
	KindBinary
)

// Value is a typed cell value. Text holds the canonical text of every kind
// except NULL and binary; Size is the byte length of a binary value.
type Value struct {
	Kind Kind
	Text string
	Size int
}

// Display returns the text shown in a cell, cut to maxRunes runes with an
// ellipsis when longer (maxRunes <= 0 means no limit). It never changes
// the value: use CopyText for the full text.
func (v Value) Display(maxRunes int) string {
	switch v.Kind {
	case KindNull:
		return "NULL"
	case KindBinary:
		return binaryPlaceholder(v.Size)
	}
	if v.Text == "" {
		return "(empty)"
	}
	s := v.Text
	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, s)
	}
	return truncate(s, maxRunes)
}

// CopyText returns the text copied to the clipboard: the full value. NULL
// copies as the empty string and binary as its placeholder.
func (v Value) CopyText() string {
	switch v.Kind {
	case KindNull:
		return ""
	case KindBinary:
		return binaryPlaceholder(v.Size)
	}
	return v.Text
}

// Equal reports whether two values have the same kind and content. It
// compares values, never their display strings.
func (v Value) Equal(o Value) bool { return v == o }

func binaryPlaceholder(size int) string { return fmt.Sprintf("<binary %d bytes>", size) }

func truncate(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) <= maxRunes {
		return s
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes-1]) + "…"
}
