package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		secrets []string
		want    string
	}{
		{"plain", "password hunter2 rejected", []string{"hunter2"}, "password ••• rejected"},
		{"empty secret changes nothing", "no secret here", []string{""}, "no secret here"},
		{"no secrets", "abc", nil, "abc"},
		{"every occurrence", "x s3cret y s3cret", []string{"s3cret"}, "x ••• y •••"},
		{"short secret", "a1b2", []string{"1"}, "a•••b2"},
		{"regex characters", "got p.a*s[s", []string{"p.a*s[s"}, "got •••"},
		{"unicode", "mật khẩu: đúng-123", []string{"đúng-123"}, "mật khẩu: •••"},
		{"url characters", "postgres://u:p@ss:w/rd@h/db", []string{"p@ss:w/rd"}, "postgres://u:•••@h/db"},
		{"longer secret first", "token abcdef", []string{"abc", "abcdef"}, "token •••"},
		{"two secrets", "a=one b=two", []string{"one", "two"}, "a=••• b=•••"},
	}
	for _, c := range cases {
		if got := Redact(c.text, c.secrets...); got != c.want {
			t.Errorf("%s: Redact(%q, %q) = %q, want %q", c.name, c.text, c.secrets, got, c.want)
		}
	}
}

func TestEmptySecretNeverInsertsMasks(t *testing.T) {
	if got := Redact("abc", ""); strings.Contains(got, "•") {
		t.Errorf("an empty secret masked the text: %q", got)
	}
}

func TestErrorShowsOnlyItsMessage(t *testing.T) {
	cause := fmt.Errorf("server said password=%s", "hunter2")
	err := error(&Error{Kind: KindAuthentication, Message: "Authentication failed for user bob", Err: cause})
	if got := err.Error(); strings.Contains(got, "hunter2") {
		t.Errorf("Error() leaks the cause: %q", got)
	}
	wrapped := fmt.Errorf("connect: %w", err)
	if KindOf(wrapped) != KindAuthentication {
		t.Errorf("KindOf(wrapped) = %v", KindOf(wrapped))
	}
	if !errors.Is(&Error{Kind: KindCanceled, Err: context.Canceled}, context.Canceled) {
		t.Error("the cause is not reachable with errors.Is")
	}
	if KindOf(errors.New("plain")) != 0 {
		t.Error("a plain error has a kind")
	}
}

func TestEveryKindHasAName(t *testing.T) {
	for k := KindConnection; k <= KindFileAccess; k++ {
		if k.String() == "Error" || k.String() == "" {
			t.Errorf("kind %d has no name", k)
		}
	}
	if Kind(0).String() != "Error" {
		t.Error("the zero kind should have the generic name")
	}
}
