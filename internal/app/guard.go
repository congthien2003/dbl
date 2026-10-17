package app

import "errors"

// Errors of the actions that replace the rows of a table tab.
var (
	// ErrEditsPending: the tab has unsaved edits, so the action waits for the
	// user to Save, Discard or Cancel (State.PendingEdit). Nothing has
	// changed yet.
	ErrEditsPending = errors.New("the table has unsaved edits: save or discard them first")
	// ErrSaving: a Save of the tab is running, so the action is refused.
	ErrSaving = errors.New("the table is being saved")
)

// PendingEdit is an action that would replace the rows of a table tab that has
// unsaved edits, held back until the user decides (spec FR-005, pending edit
// protection). Run does the action.
type PendingEdit struct {
	Tab    TabID
	Reason string // "change the filter", "go to the next page"…
	Run    func()
}

// EditChoice is the user's answer to a PendingEdit.
type EditChoice uint8

// Answers to a PendingEdit.
const (
	EditCancel EditChoice = iota
	EditDiscard
	EditSave
)

// guarded runs an action that replaces the loaded rows of tab, unless the tab
// has unsaved edits: then the action is held in PendingEdit and
// ErrEditsPending is returned. While the tab is being saved the action is
// refused with ErrSaving.
func (s *State) guarded(tab *Tab, reason string, run func()) error {
	if tab.Saving() {
		return ErrSaving
	}
	if !tab.Dirty() {
		run()
		return nil
	}
	s.PendingEdit = &PendingEdit{Tab: tab.ID, Reason: reason, Run: run}
	return ErrEditsPending
}

// ResolvePendingEdit carries out the user's answer. Discard drops the edits
// and does the action; Save does the action once the changes are committed
// (a failed Save leaves everything as it was); Cancel does nothing.
func (s *State) ResolvePendingEdit(choice EditChoice) {
	pe := s.PendingEdit
	s.PendingEdit = nil
	if pe == nil {
		return
	}
	tab := s.Workspace.Find(pe.Tab)
	if tab == nil {
		return
	}
	switch choice {
	case EditDiscard:
		s.RevertEdits(tab)
		pe.Run()
	case EditSave:
		s.Save(tab, pe.Run)
	}
}
