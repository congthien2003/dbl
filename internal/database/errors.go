package database

import (
	"errors"
	"sort"
	"strings"
)

// Kind is the stable category of an error (spec 9).
type Kind uint8

// Error categories.
const (
	KindConnection          Kind = iota + 1 // connection failure
	KindAuthentication                      // authentication failure
	KindPermission                          // permission denied
	KindSyntax                              // query syntax error
	KindTimeout                             // query or connection timeout
	KindCanceled                            // canceled by the user
	KindMetadataUnavailable                 // metadata unavailable
	KindUnsupported                         // unsupported operation
	KindValidation                          // data validation error
	KindConflict                            // concurrent modification
	KindSave                                // save failure
	KindFileAccess                          // file access failure
)

var kindNames = map[Kind]string{
	KindConnection:          "Connection failure",
	KindAuthentication:      "Authentication failure",
	KindPermission:          "Permission denied",
	KindSyntax:              "Query syntax error",
	KindTimeout:             "Timeout",
	KindCanceled:            "Canceled",
	KindMetadataUnavailable: "Metadata unavailable",
	KindUnsupported:         "Unsupported operation",
	KindValidation:          "Data validation error",
	KindConflict:            "Concurrent modification",
	KindSave:                "Save failure",
	KindFileAccess:          "File access failure",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "Error"
}

// Error is an error with a stable category. Message is the text to show the
// user; it never holds a password. Err is the cause, for errors.Is and
// errors.As only: it may hold text from the server, so it is never shown.
type Error struct {
	Kind    Kind
	Message string
	// Hint suggests what the user can try, such as another TLS mode.
	Hint string
	Err  error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the category of err, or 0 when it is not an *Error.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

// maskText replaces a secret in text.
const maskText = "•••"

// Redact returns text without any of the secrets: every occurrence is
// replaced by a mask. Empty secrets are ignored (replacing "" would insert
// the mask between every character), and longer secrets go first so one
// that contains another is hidden whole.
func Redact(text string, secrets ...string) string {
	var list []string
	for _, s := range secrets {
		if s != "" {
			list = append(list, s)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, s := range list {
		text = strings.ReplaceAll(text, s, maskText)
	}
	return text
}
