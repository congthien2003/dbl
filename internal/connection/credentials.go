package connection

import "errors"

// Errors a CredentialStore reports.
var (
	// ErrSecretNotFound means no password is stored under the reference.
	ErrSecretNotFound = errors.New("no saved password")
	// ErrCredentialsUnavailable means the system has no credential store, or
	// it could not be used.
	ErrCredentialsUnavailable = errors.New("credential store unavailable")
)

// CredentialStore keeps passwords outside the profiles file. Its methods
// may block (the system can ask the user to unlock the store), so only a
// background goroutine calls them.
type CredentialStore interface {
	Get(ref string) (password string, err error)
	Set(ref, password string) error
	// Delete removes the password; deleting a missing one is not an error.
	Delete(ref string) error
}

// SecretPrefix starts every SecretRef.
const SecretPrefix = "dbl/"

// SecretRefFor returns the credential reference of the profile with the
// given ID. It is derived from the ID, never from the name.
func SecretRefFor(profileID string) string { return SecretPrefix + profileID }

// PasswordMode says what saving a profile does with its password.
type PasswordMode uint8

// Password modes. The edit form leaves the field empty and the mode at
// KeepPassword; typing a value gives SetPassword; the "Remove saved
// password" box gives RemovePassword.
const (
	KeepPassword PasswordMode = iota
	SetPassword
	RemovePassword
)

// PasswordChange is the password part of saving a profile.
type PasswordChange struct {
	Mode  PasswordMode
	Value string // for SetPassword
}
