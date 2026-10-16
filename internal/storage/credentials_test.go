package storage

import (
	"errors"
	"fmt"
	"testing"

	"github.com/egoist/mygo"

	"dbl/internal/connection"
)

func TestMapSecretError(t *testing.T) {
	if mapSecretError(nil) != nil {
		t.Error("nil became an error")
	}
	if err := mapSecretError(mygo.ErrSecretNotFound); !errors.Is(err, connection.ErrSecretNotFound) {
		t.Errorf("not found: %v", err)
	}
	wrapped := fmt.Errorf("mygo: secrets: %w", mygo.ErrSecretNotFound)
	if err := mapSecretError(wrapped); !errors.Is(err, connection.ErrSecretNotFound) {
		t.Errorf("wrapped not found: %v", err)
	}
	unsupported := fmt.Errorf("mygo: secrets: %w", errors.ErrUnsupported)
	err := mapSecretError(unsupported)
	if !errors.Is(err, connection.ErrCredentialsUnavailable) || errors.Is(err, connection.ErrSecretNotFound) {
		t.Errorf("no credential store: %v", err)
	}
	if err := mapSecretError(errors.New("keyring is locked")); !errors.Is(err, connection.ErrCredentialsUnavailable) {
		t.Errorf("any other failure should mean unavailable: %v", err)
	}
	if err := mapSecretError(connection.ErrCredentialsUnavailable); !errors.Is(err, connection.ErrCredentialsUnavailable) {
		t.Errorf("an already-mapped error changed: %v", err)
	}
}

func TestCredentialServiceIsFixed(t *testing.T) {
	// The service must not follow the app identifier; see CredentialService.
	if CredentialService != "DBL" {
		t.Errorf("CredentialService = %q", CredentialService)
	}
}
