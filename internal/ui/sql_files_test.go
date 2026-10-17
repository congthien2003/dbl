package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbl/internal/app"
	"dbl/internal/database"
	"dbl/internal/database/postgres"
	"dbl/internal/storage"
)

func TestSQLBufferCRLFBOMUnicodeRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "query.sql")
	original := append([]byte{0xef, 0xbb, 0xbf}, []byte("SELECT 'tiếng Việt 😀';\r\nSELECT 2;\r\n")...)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := storage.NewSQLStore(root).Read(path)
	if err != nil {
		t.Fatal(err)
	}
	sh, tt := newTestShell(t)
	tab := sh.st.Workspace.NewSQL("")
	tab.SQL.Document = app.SQLDocument{Text: file.Text, SavedText: file.Text, File: file, Revision: 1}
	tt.Frame()
	view := sh.views[tab.ID].sql
	start := strings.Index(file.Text, "SELECT 2")
	view.state.Select(start, len(file.Text))
	input := database.SQLInput{Text: view.state.Buffer.String(), Cursor: view.state.Cursor()}
	for _, sel := range view.state.Selections() {
		input.Selections = append(input.Selections, database.SQLSpan{Start: sel.Start(), End: sel.End()})
	}
	selected, err := postgres.SelectPostgresSQL(input, true)
	if err != nil || strings.TrimSpace(selected.SQL) != "SELECT 2;" {
		t.Fatal("Unicode offsets did not select the second statement", selected, err)
	}
	sh.SyncSQLDocuments()
	encoded, err := storage.EncodeSQL(tab.SQL.Document.File, tab.SQL.Document.Text)
	if err != nil || tab.Dirty() || !bytes.Equal(encoded, original) {
		t.Fatal("opening unchanged BOM/CRLF file modified its document", err)
	}
}

func TestSQLQuitGuardUsedForAppAndWindowClose(t *testing.T) {
	for _, entry := range []string{"window close", "application quit"} {
		t.Run(entry, func(t *testing.T) {
			sh, tt := newTestShell(t)
			tab := sh.st.Workspace.NewSQL("")
			tt.Frame()
			sh.st.SetSQLText(tab, "select 1")
			if sh.RequestQuit() || sh.st.PendingSQL == nil {
				t.Fatal("shared quit boundary bypassed document guard")
			}
			sh.st.ResolveSQLAction(app.SQLActionCancel)
			if sh.st.Workspace.Find(tab.ID) == nil {
				t.Fatal("canceled quit closed document")
			}
		})
	}
}
