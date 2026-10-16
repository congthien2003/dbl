package app

import (
	"errors"
	"fmt"
	"time"

	"dbl/internal/connection"
)

// ErrConnectionsReadOnly is returned by every change to the catalog while
// connections.json must not be written.
var ErrConnectionsReadOnly = errors.New("connections are read-only")

// ConnActionKind is what a PendingConn will do once the user agrees.
type ConnActionKind uint8

// Pending connection actions.
const (
	ConnDisconnect ConnActionKind = iota + 1 // close the session and the tabs of the connection
	ConnDelete                               // disconnect, remove the profile and its saved password
	ConnApplyEdit                            // disconnect, then save the edited profile
)

// PendingConn is a connection change that waits for the user's answer
// because it ends the session of the connection. The UI asks, then calls
// ConfirmConn or CancelConn.
type PendingConn struct {
	Kind         ConnActionKind
	ConnectionID string
	Name         string
	Tabs         int // tabs open on the connection, which will be closed
	DirtyTabs    int // of them, tabs with unsaved data, which will be lost

	// For ConnApplyEdit: the profile to save, and what to do with its
	// password.
	Profile  connection.Profile
	Password connection.PasswordChange
}

// editable refuses a change while connections.json is read-only.
func (s *State) editable() error {
	if s.ConnectionsReadOnly == "" {
		return nil
	}
	s.Notice = "Connections are read-only: " + s.ConnectionsReadOnly
	return fmt.Errorf("%w: %s", ErrConnectionsReadOnly, s.ConnectionsReadOnly)
}

func (s *State) persistCatalog() {
	if s.ScheduleCatalogSave != nil {
		s.ScheduleCatalogSave(s.Catalog.Snapshot())
	}
}

// SetCatalogSaveError shows that connections.json could not be written. The
// change stays in memory and is written again with the next change.
func (s *State) SetCatalogSaveError(err error) {
	s.catalogNotice = "Could not save connections: " + err.Error()
	s.Notice = s.catalogNotice
}

// ClearCatalogSaveError removes the message of SetCatalogSaveError after a
// write succeeded.
func (s *State) ClearCatalogSaveError() {
	if s.catalogNotice != "" && s.Notice == s.catalogNotice {
		s.Notice = ""
	}
	s.catalogNotice = ""
}

// ReportCredentialIssue tells the user a password could not be saved or
// removed. id is empty when it could not be removed.
func (s *State) ReportCredentialIssue(id string, err error) {
	if id == "" {
		s.Notice = "A saved password could not be removed from the credential store: " + err.Error()
		return
	}
	name := id
	if p, ok := s.Catalog.Profile(id); ok {
		name = p.Name
	}
	s.Notice = fmt.Sprintf("The password of %q could not be saved in the credential store; it is kept for this session only.", name)
}

// ---- Folders ----

// AddFolder creates a folder.
func (s *State) AddFolder(name string) (connection.Folder, error) {
	if err := s.editable(); err != nil {
		return connection.Folder{}, err
	}
	f, err := s.Catalog.AddFolder(name)
	if err == nil {
		s.persistCatalog()
	}
	return f, err
}

// RenameFolder renames a folder.
func (s *State) RenameFolder(id, name string) error {
	if err := s.editable(); err != nil {
		return err
	}
	err := s.Catalog.RenameFolder(id, name)
	if err == nil {
		s.persistCatalog()
	}
	return err
}

// RequestDeleteFolder deletes an empty folder at once. A folder with
// connections waits in PendingFolderDelete: the user chooses between moving
// its connections to the root (ConfirmDeleteFolder) and keeping the folder
// (CancelDeleteFolder). It reports whether the folder is gone.
func (s *State) RequestDeleteFolder(id string) (deleted bool) {
	if err := s.editable(); err != nil {
		return false
	}
	if _, ok := s.Catalog.Folder(id); !ok {
		return false
	}
	if len(s.Catalog.ProfilesIn(id)) > 0 {
		s.PendingFolderDelete = id
		return false
	}
	if _, err := s.Catalog.DeleteFolder(id); err != nil {
		return false
	}
	s.persistCatalog()
	return true
}

// ConfirmDeleteFolder deletes the folder in PendingFolderDelete and moves
// its connections to the root.
func (s *State) ConfirmDeleteFolder() {
	id := s.PendingFolderDelete
	s.PendingFolderDelete = ""
	if id == "" || s.editable() != nil {
		return
	}
	if _, err := s.Catalog.DeleteFolder(id); err == nil {
		s.persistCatalog()
	}
}

// CancelDeleteFolder keeps the folder.
func (s *State) CancelDeleteFolder() { s.PendingFolderDelete = "" }

// ---- Connections ----

// MoveConnection moves a connection into a folder, or to the root for "".
// It never touches the session.
func (s *State) MoveConnection(id, folderID string) error {
	if err := s.editable(); err != nil {
		return err
	}
	err := s.Catalog.MoveProfile(id, folderID)
	if err == nil {
		s.persistCatalog()
	}
	return err
}

// RenameConnection renames a connection. The session and its tabs are
// untouched.
func (s *State) RenameConnection(id, name string) error {
	if err := s.editable(); err != nil {
		return err
	}
	err := s.Catalog.RenameProfile(id, name)
	if err == nil {
		s.persistCatalog()
	}
	return err
}

// affectsSession reports whether saving b over a changes what an open
// session was made with, so it must end.
func affectsSession(a, b connection.Profile, c connection.PasswordChange) bool {
	return a.Host != b.Host || a.Port != b.Port || a.Database != b.Database || a.Username != b.Username ||
		a.TLS != b.TLS || a.ConnectTimeoutSec != b.ConnectTimeoutSec || a.Pool != b.Pool ||
		a.SecretRef != b.SecretRef || c.Mode == connection.RemovePassword ||
		(c.Mode == connection.SetPassword && c.Value != "")
}

func fieldError(field, msg string) error {
	return &connection.ValidationError{Fields: []connection.FieldError{{Field: field, Message: msg}}}
}

// SaveProfile creates the profile (when p.ID is empty or unknown) or saves
// the edit of an existing one. change says what to do with the password:
// the edit form leaves it at KeepPassword unless the user typed one or
// ticked "Remove saved password".
//
// A validation problem returns a *connection.ValidationError and changes
// nothing. When the edit would end the session of a connected or connecting
// profile, nothing is saved yet: PendingConn asks the user first and
// applied is false.
func (s *State) SaveProfile(p connection.Profile, change connection.PasswordChange) (applied bool, err error) {
	if err := s.editable(); err != nil {
		return false, err
	}
	p = p.Normalized()
	errs := append(p.Validate(), p.ValidateFiles()...)
	if change.Mode == connection.SetPassword {
		if fe := connection.ValidatePassword(change.Value); fe != nil {
			errs = append(errs, *fe)
		}
	}
	if len(errs) > 0 {
		return false, &connection.ValidationError{Fields: errs}
	}
	if change.Mode == connection.SetPassword && change.Value == "" {
		change.Mode = connection.KeepPassword
	}

	if p.ID == "" {
		return s.createProfile(p, change)
	}
	existing, exists := s.Catalog.Profile(p.ID)
	if !exists {
		// The connection was deleted while its form was open: saving must not
		// bring it back.
		return false, connection.ErrNotFound
	}
	if !existing.Driver.Supported() {
		return false, fieldError(connection.FieldDriver, fmt.Sprintf("%s connections cannot be edited in this version", existing.Driver.Label()))
	}
	switch change.Mode {
	case connection.KeepPassword:
		p.SecretRef = existing.SecretRef
	case connection.SetPassword:
		p.SecretRef = connection.SecretRefFor(p.ID)
	case connection.RemovePassword:
		p.SecretRef = ""
	}
	if st := s.Conns.Status(p.ID); (st == connection.Connecting || st == connection.Connected) && affectsSession(existing, p, change) {
		tabs := s.Workspace.TabsOf(p.ID)
		dirty := 0
		for _, t := range tabs {
			if t.Dirty() {
				dirty++
			}
		}
		s.PendingConn = &PendingConn{
			Kind: ConnApplyEdit, ConnectionID: p.ID, Name: existing.Name,
			Tabs: len(tabs), DirtyTabs: dirty, Profile: p, Password: change,
		}
		return false, nil
	}
	if err := s.applyEdit(existing, p, change); err != nil {
		return false, err
	}
	return true, nil
}

func (s *State) createProfile(p connection.Profile, change connection.PasswordChange) (bool, error) {
	if p.ID == "" {
		p.ID = connection.NewID()
	}
	p.SecretRef = ""
	if change.Mode == connection.SetPassword {
		p.SecretRef = connection.SecretRefFor(p.ID)
	}
	added, err := s.Catalog.AddProfile(p)
	if err != nil {
		return false, err
	}
	s.persistCatalog()
	if added.SecretRef != "" {
		s.Conns.StorePassword(added.ID, change.Value)
	}
	return true, nil
}

// applyEdit saves the edited profile and carries out the password change.
// The session, when there was one, is already closed by the caller.
func (s *State) applyEdit(existing, p connection.Profile, change connection.PasswordChange) error {
	if _, err := s.Catalog.UpdateProfile(p); err != nil {
		return err
	}
	s.persistCatalog()
	switch change.Mode {
	case connection.SetPassword:
		s.Conns.StorePassword(p.ID, change.Value)
	case connection.RemovePassword:
		s.Conns.ClearSessionPassword(p.ID)
		s.Conns.DeleteStoredPassword(existing.SecretRef)
	}
	if affectsSession(existing, p, change) {
		// Forget a failure or a password prompt that belonged to the old
		// settings; a live session was ended by the caller.
		s.Conns.Disconnect(p.ID)
	}
	return nil
}

// closeTabsOf closes the tabs of a connection, dropping a close
// confirmation that was waiting for one of them.
func (s *State) closeTabsOf(id string) {
	s.Workspace.CloseConnection(id)
	if s.PendingClose != 0 && s.Workspace.Find(s.PendingClose) == nil {
		s.PendingClose = 0
	}
}

// RequestDisconnect ends the session of a connection. With tabs open on it,
// PendingConn asks first and done is false.
func (s *State) RequestDisconnect(id string) (done bool) {
	tabs := s.Workspace.TabsOf(id)
	if len(tabs) == 0 {
		s.Conns.Disconnect(id)
		return true
	}
	dirty := 0
	for _, t := range tabs {
		if t.Dirty() {
			dirty++
		}
	}
	name := id
	if p, ok := s.Catalog.Profile(id); ok {
		name = p.Name
	}
	s.PendingConn = &PendingConn{Kind: ConnDisconnect, ConnectionID: id, Name: name, Tabs: len(tabs), DirtyTabs: dirty}
	return false
}

// RequestDelete asks to delete a connection. Deleting is irreversible (the
// saved password goes too), so the user is always asked.
func (s *State) RequestDelete(id string) {
	if err := s.editable(); err != nil {
		return
	}
	p, ok := s.Catalog.Profile(id)
	if !ok {
		return
	}
	tabs := s.Workspace.TabsOf(id)
	dirty := 0
	for _, t := range tabs {
		if t.Dirty() {
			dirty++
		}
	}
	s.PendingConn = &PendingConn{Kind: ConnDelete, ConnectionID: id, Name: p.Name, Tabs: len(tabs), DirtyTabs: dirty}
}

// ConfirmConn carries out PendingConn: the attempt is cancelled, the tabs of
// the connection are closed, the session is closed, and only then is the
// change applied.
func (s *State) ConfirmConn() {
	pc := s.PendingConn
	s.PendingConn = nil
	if pc == nil {
		return
	}
	switch pc.Kind {
	case ConnDisconnect:
		s.closeTabsOf(pc.ConnectionID)
		s.Conns.Disconnect(pc.ConnectionID)
	case ConnDelete:
		if s.editable() != nil {
			return
		}
		s.closeTabsOf(pc.ConnectionID)
		s.Conns.Forget(pc.ConnectionID)
		removed, err := s.Catalog.DeleteProfile(pc.ConnectionID)
		if err != nil {
			return
		}
		s.persistCatalog()
		s.Conns.DeleteStoredPassword(removed.SecretRef)
	case ConnApplyEdit:
		if s.editable() != nil {
			return
		}
		existing, ok := s.Catalog.Profile(pc.ConnectionID)
		if !ok {
			return
		}
		s.closeTabsOf(pc.ConnectionID)
		s.Conns.Disconnect(pc.ConnectionID)
		if err := s.applyEdit(existing, pc.Profile, pc.Password); err != nil {
			s.Notice = "The connection could not be saved: " + err.Error()
		}
	}
}

// CancelConn drops PendingConn and changes nothing.
func (s *State) CancelConn() { s.PendingConn = nil }

// Connect starts connecting a saved connection.
func (s *State) Connect(id string) {
	s.TestNotice = ""
	s.Conns.Connect(id)
}

// CancelConnect cancels a running connection attempt.
func (s *State) CancelConnect(id string) { s.Conns.Disconnect(id) }

// SubmitPassword answers the password prompt of a connection.
func (s *State) SubmitPassword(id, password string) { s.Conns.SubmitPassword(id, password) }

// CancelPasswordPrompt dismisses the password prompt.
func (s *State) CancelPasswordPrompt(id string) { s.Conns.CancelPrompt(id) }

// TestSaved tests a saved connection with its saved settings and shows the
// outcome in TestNotice.
func (s *State) TestSaved(id string) {
	p, ok := s.Catalog.Profile(id)
	if !ok {
		return
	}
	if s.Conns == nil {
		s.TestNotice = "Connections are not available."
		return
	}
	if s.cancelTest != nil {
		s.cancelTest()
	}
	s.TestNotice = fmt.Sprintf("Testing %q…", p.Name)
	s.cancelTest = s.Conns.Test(connection.TestRequest{Profile: p, ExistingID: id}, func(res connection.TestResult) {
		s.cancelTest = nil
		switch {
		case res.OK:
			s.TestNotice = fmt.Sprintf("Test of %q succeeded in %d ms.", p.Name, res.Duration.Round(time.Millisecond).Milliseconds())
		case res.NeedsPassword:
			s.TestNotice = fmt.Sprintf("Test of %q needs a password: %s Connect once to enter it.", p.Name, res.Reason)
		case res.Err != nil:
			s.TestNotice = fmt.Sprintf("Test of %q failed: %s", p.Name, res.Err.Message)
			if res.Err.Hint != "" {
				s.TestNotice += " " + res.Err.Hint
			}
		}
	})
}
