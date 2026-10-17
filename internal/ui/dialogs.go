package ui

import (
	"errors"
	"fmt"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/connection"
)

// ---- Name dialog: new folder, rename folder, rename connection ----

type nameKind uint8

const (
	nameNewFolder nameKind = iota + 1
	nameRenameFolder
	nameRenameConnection
)

type nameDialog struct {
	kind nameKind
	id   string // the folder or connection being renamed
	text string
	err  string
}

func (sh *Shell) openNameDialog(kind nameKind, id, text string) {
	sh.nameDlg = &nameDialog{kind: kind, id: id, text: text}
}

// errorText is the message to show for an error of a State action.
func errorText(err error) string {
	var ve *connection.ValidationError
	if errors.As(err, &ve) && len(ve.Fields) > 0 {
		return ve.Fields[0].Message
	}
	return err.Error()
}

func (sh *Shell) nameDialogView(c *mygoui.Context, t *mygoui.Theme) {
	d := sh.nameDlg
	if d == nil {
		return
	}
	title, ok := "New folder", "Create"
	switch d.kind {
	case nameRenameFolder:
		title, ok = "Rename folder", "Rename"
	case nameRenameConnection:
		title, ok = "Rename connection", "Rename"
	}
	open, submit := true, false
	mygoui.Modal(c, &open, func() {
		mygoui.Column(c).Width(380).Padding(20).Gap(12).Children(func() {
			mygoui.Text(c, title).FontSize(16).Bold()
			mygoui.Field(c, "Name", func() {
				if mygoui.TextInput(c, &d.text).AutoFocus().Submitted() {
					submit = true
				}
			}).Error(d.err)
			mygoui.Row(c).Gap(8).Justify(mygoui.End).Children(func() {
				if sh.fade(mygoui.Button(c, "Cancel")).Clicked() {
					open = false
				}
				if sh.fade(mygoui.PrimaryButton(c, ok)).Clicked() {
					submit = true
				}
			})
		})
	})
	switch {
	case submit:
		if err := sh.applyName(d); err != nil {
			d.err = errorText(err)
		} else {
			sh.nameDlg = nil
		}
		c.Invalidate()
	case !open:
		sh.nameDlg = nil
		c.Invalidate()
	}
}

func (sh *Shell) applyName(d *nameDialog) error {
	switch d.kind {
	case nameNewFolder:
		_, err := sh.st.AddFolder(d.text)
		return err
	case nameRenameFolder:
		return sh.st.RenameFolder(d.id, d.text)
	case nameRenameConnection:
		return sh.st.RenameConnection(d.id, d.text)
	}
	return nil
}

// ---- Password prompt ----

type pwPrompt struct {
	id   string
	text string
}

// promptID returns the first connection waiting for a password.
func (sh *Shell) promptID() string {
	for _, p := range sh.st.Catalog.Profiles {
		if sh.st.Conns.Status(p.ID) == connection.NeedsPassword {
			return p.ID
		}
	}
	return ""
}

func (sh *Shell) passwordPromptView(c *mygoui.Context, t *mygoui.Theme) {
	id := sh.promptID()
	if id == "" {
		sh.pw = pwPrompt{}
		return
	}
	// Wait for the other dialogs: the prompt stays pending meanwhile.
	if sh.form != nil || sh.nameDlg != nil || sh.st.PendingConn != nil || sh.st.PendingFolderDelete != "" {
		return
	}
	if sh.pw.id != id {
		sh.pw = pwPrompt{id: id}
	}
	p, _ := sh.st.Catalog.Profile(id)
	open, submit := true, false
	mygoui.Modal(c, &open, func() {
		mygoui.Column(c).Width(400).Padding(20).Gap(12).Children(func() {
			mygoui.Text(c, "Password for "+p.Name).FontSize(16).Bold()
			if reason := sh.st.Conns.PromptReason(id); reason != "" {
				mygoui.Text(c, reason).FontSize(12).TextColor(t.TextMuted)
			}
			mygoui.Text(c, "It is kept in memory until you quit and is not written to disk.").FontSize(12).TextColor(t.TextMuted)
			mygoui.Field(c, "Password", func() {
				if mygoui.TextInput(c, &sh.pw.text).Password().AutoFocus().Submitted() {
					submit = true
				}
			})
			mygoui.Row(c).Gap(8).Justify(mygoui.End).Children(func() {
				if sh.fade(mygoui.Button(c, "Cancel")).Clicked() {
					open = false
				}
				if sh.fade(mygoui.PrimaryButton(c, "Connect")).Clicked() {
					submit = true
				}
			})
		})
	})
	switch {
	case submit:
		pw := sh.pw.text
		sh.pw = pwPrompt{}
		sh.st.SubmitPassword(id, pw)
		c.Invalidate()
	case !open:
		sh.pw = pwPrompt{}
		sh.st.CancelPasswordPrompt(id)
		c.Invalidate()
	}
}

// ---- Confirmations ----

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// tabsNote says what happens to the tabs of a connection.
func tabsNote(pc *app.PendingConn) string {
	if pc.Tabs == 0 {
		return ""
	}
	note := fmt.Sprintf(" %s open on it will be closed", plural(pc.Tabs, "tab"))
	if pc.DirtyTabs > 0 {
		note += fmt.Sprintf(", and the unsaved changes in %d of them will be lost", pc.DirtyTabs)
	}
	note += "."
	if pc.SavingTabs > 0 {
		note += savingNote(pc.SavingTabs)
	}
	return note
}

// connectionDialogs asks about the connection changes the state holds back.
// At most one dialog shows at a time.
func (sh *Shell) connectionDialogs(c *mygoui.Context) {
	st := sh.st
	if pc := st.PendingConn; pc != nil {
		var title, msg, confirm string
		switch pc.Kind {
		case app.ConnDisconnect:
			title = fmt.Sprintf("Disconnect %q?", pc.Name)
			msg = "The session will be closed." + tabsNote(pc)
			confirm = "Disconnect"
		case app.ConnDelete:
			title = fmt.Sprintf("Delete %q?", pc.Name)
			msg = "This removes the saved connection and its saved password. It cannot be undone." + tabsNote(pc)
			confirm = "Delete"
		case app.ConnApplyEdit:
			title = fmt.Sprintf("Save changes to %q?", pc.Name)
			msg = "The connection is open. Saving these changes disconnects it." + tabsNote(pc)
			confirm = "Save and disconnect"
		}
		buttons := []string{"Cancel", confirm}
		if pc.DirtyTabs > 0 && canSave(st.Workspace.TabsOf(pc.ConnectionID)) {
			buttons = append(buttons, "Save changes first")
		}
		open := true
		switch mygoui.AlertDialog(c, &open, title, msg, buttons...) {
		case 1:
			st.ConfirmConn()
			c.Invalidate()
		case 2:
			st.SaveAndConfirmConn()
			c.Invalidate()
		case 0, mygoui.AlertDismissed:
			st.CancelConn()
			c.Invalidate()
		}
		return
	}
	if id := st.PendingFolderDelete; id != "" {
		f, _ := st.Catalog.Folder(id)
		n := len(st.Catalog.ProfilesIn(id))
		open := true
		switch mygoui.AlertDialog(c, &open, fmt.Sprintf("Delete folder %q?", f.Name),
			fmt.Sprintf("%s in it will move to the top level.", plural(n, "connection")),
			"Cancel", "Move to top level and delete") {
		case 1:
			st.ConfirmDeleteFolder()
			c.Invalidate()
		case 0, mygoui.AlertDismissed:
			st.CancelDeleteFolder()
			c.Invalidate()
		}
	}
}
