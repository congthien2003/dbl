package storage

import (
	"errors"
	"fmt"

	"github.com/egoist/mygo"

	"dbl/internal/connection"
)

// CredentialService is the credential store service every saved password
// lives under. It is fixed, not the app identifier: the identifier changes
// between `mygo dev`, `go run` and an installed app (and when it is renamed
// for release), which would orphan the passwords already saved.
const CredentialService = "DBL"

// SystemCredentials keeps passwords in the system's credential store (the
// Windows Credential Manager, the macOS Keychain or the Linux Secret
// Service) through MyGo. Its methods may block while the system asks the
// user to unlock the store, so call them from a background goroutine only.
type SystemCredentials struct{}

var _ connection.CredentialStore = SystemCredentials{}

func (SystemCredentials) secrets() mygo.SecretsModule { return mygo.Secrets.Service(CredentialService) }

// Get returns the password saved under ref.
func (s SystemCredentials) Get(ref string) (string, error) {
	b, err := s.secrets().Get(ref)
	if err != nil {
		return "", mapSecretError(err)
	}
	return string(b), nil
}

// Set saves the password under ref, replacing the previous one.
func (s SystemCredentials) Set(ref, password string) error {
	return mapSecretError(s.secrets().Set(ref, []byte(password)))
}

// Delete removes the password saved under ref.
func (s SystemCredentials) Delete(ref string) error {
	return mapSecretError(s.secrets().Delete(ref))
}

// mapSecretError turns MyGo's errors into the connection package's: a
// missing secret is ErrSecretNotFound, and any other failure, including a
// system without a credential store, means the store is unavailable.
func mapSecretError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mygo.ErrSecretNotFound):
		return connection.ErrSecretNotFound
	case errors.Is(err, connection.ErrSecretNotFound), errors.Is(err, connection.ErrCredentialsUnavailable):
		return err
	}
	return fmt.Errorf("%w: %v", connection.ErrCredentialsUnavailable, err)
}
