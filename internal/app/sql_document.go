package app

import "dbl/internal/storage"

type SQLDocument struct {
	Text, SavedText string
	Revision        uint64
	File            *storage.SQLFile
	FileState       OpState
	FileErr         error
	fileOp          *sqlFileOp
}

func (d *SQLDocument) Dirty() bool { return d.Text != d.SavedText }
func (s *State) SetSQLText(tab *Tab, text string) {
	if tab == nil || tab.SQL == nil || tab.SQL.Document.Text == text {
		return
	}
	tab.SQL.Document.Text = text
	tab.SQL.Document.Revision++
}
