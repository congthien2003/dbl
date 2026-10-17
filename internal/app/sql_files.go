package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"dbl/internal/database"
	"dbl/internal/query"
	"dbl/internal/storage"
)

type SQLDialogs interface {
	OpenSQL() (string, error)
	SaveSQL(string) (string, error)
}
type SQLServices struct {
	Executor     *query.Executor
	Files        storage.SQLFiles
	Dialogs      SQLDialogs
	life         context.Context
	stop         context.CancelFunc
	dialog       atomic.Bool
	reservations map[TabID]storage.FileRevision // UI-thread owned
}

func NewSQLServices(files storage.SQLFiles, dialogs SQLDialogs) *SQLServices {
	life, stop := context.WithCancel(context.Background())
	return &SQLServices{Executor: query.NewExecutor(), Files: files, Dialogs: dialogs, life: life, stop: stop, reservations: map[TabID]storage.FileRevision{}}
}
func (s *SQLServices) DialogBusy() bool { return s != nil && s.dialog.Load() }
func (s *State) CloseSQL(ctx context.Context) error {
	if s.SQLServices == nil {
		return nil
	}
	s.SQLServices.stop()
	cleanup, cancel := context.WithTimeout(ctx, database.SQLCleanupTimeout)
	defer cancel()
	return s.SQLServices.Executor.Close(cleanup)
}

type sqlFileOp struct {
	text     string
	path     string
	expected storage.FileRevision
	then     func()
	conflict bool
}

func (s *State) OpenSQLFile() {
	svc := s.SQLServices
	if svc == nil || svc.Dialogs == nil || svc.Files == nil || !svc.dialog.CompareAndSwap(false, true) {
		return
	}
	go func() {
		path, err := svc.Dialogs.OpenSQL()
		svc.dialog.Store(false)
		var file *storage.SQLFile
		if err == nil && path != "" {
			file, err = svc.Files.Read(path)
		}
		s.post(func() {
			if svc.life.Err() != nil {
				return
			}
			if err != nil {
				s.Notice = "Open SQL file: " + err.Error()
				return
			}
			if file == nil {
				return
			}
			for _, tab := range s.Workspace.Tabs {
				if tab.SQL != nil && tab.SQL.Document.File != nil && tab.SQL.Document.File.Revision.Identity.Same(file.Revision.Identity) {
					s.Workspace.Select(tab.ID)
					return
				}
			}
			for _, reserved := range svc.reservations {
				if reserved.Identity.Same(file.Revision.Identity) {
					s.Notice = "This SQL file is being saved by another tab. Try opening it after the save finishes."
					return
				}
			}
			connectionID := ""
			if current := s.Workspace.Active(); current != nil {
				connectionID = current.Table.ConnectionID
			}
			tab := s.Workspace.NewSQL(connectionID)
			tab.Title = filepath.Base(file.Path)
			tab.SQL.Document = SQLDocument{Text: file.Text, SavedText: file.Text, File: file, FileState: OpReady, Revision: 1}
		})
	}()
}
func (s *State) SaveSQLFile(tab *Tab, saveAs bool, then func()) bool {
	svc := s.SQLServices
	if svc == nil || svc.Files == nil || tab == nil || tab.SQL == nil {
		return false
	}
	doc := &tab.SQL.Document
	if doc.fileOp != nil {
		return false
	}
	path := ""
	if doc.File != nil && !saveAs {
		path = doc.File.Path
	}
	needsDialog := path == ""
	if needsDialog && (svc.Dialogs == nil || !svc.dialog.CompareAndSwap(false, true)) {
		return false
	}
	op := &sqlFileOp{text: doc.Text, path: path, then: then}
	doc.fileOp, doc.FileState, doc.FileErr = op, OpLoading, nil
	id := tab.ID
	suggested := tab.Title + ".sql"
	if doc.File != nil {
		suggested = doc.File.Path
	}
	go func() {
		var err error
		if needsDialog {
			path, err = svc.Dialogs.SaveSQL(suggested)
			svc.dialog.Store(false)
		}
		var revision storage.FileRevision
		if err == nil && path != "" {
			revision, err = svc.Files.Inspect(path)
		}
		s.post(func() {
			if !s.liveSQLFileOp(id, doc, op) {
				return
			}
			if err != nil || path == "" {
				s.finishSQLFile(id, doc, op, nil, err, true)
				return
			}
			op.path = revision.Identity.Path
			if err = s.reserveSQLFile(tab, revision); err != nil {
				s.finishSQLFile(id, doc, op, nil, err, false)
				return
			}
			op.expected = revision
			if needsDialog && revision.Exists || doc.File != nil && !saveAs && !sameSQLFileRevision(doc.File.Revision, revision) {
				op.conflict = true
				doc.FileErr = &storage.FileConflict{Current: revision}
				return
			}
			s.writeSQLFile(id, doc, op)
		})
	}()
	return true
}
func sameSQLFileRevision(a, b storage.FileRevision) bool { return a.Same(b) }
func (s *State) reserveSQLFile(tab *Tab, revision storage.FileRevision) error {
	for _, other := range s.Workspace.Tabs {
		if other.ID != tab.ID && other.SQL != nil && other.SQL.Document.File != nil && other.SQL.Document.File.Revision.Identity.Same(revision.Identity) {
			return fmt.Errorf("The SQL file is already open in %s. Choose another filename.", other.Title)
		}
	}
	for id, held := range s.SQLServices.reservations {
		if id != tab.ID && held.Identity.Same(revision.Identity) {
			return errors.New("Another SQL tab is saving this file. Choose another filename.")
		}
	}
	s.SQLServices.reservations[tab.ID] = revision
	return nil
}
func (s *State) liveSQLFileOp(id TabID, doc *SQLDocument, op *sqlFileOp) bool {
	tab := s.Workspace.Find(id)
	return s.SQLServices != nil && s.SQLServices.life.Err() == nil && tab != nil && tab.SQL != nil && &tab.SQL.Document == doc && doc.fileOp == op
}
func (s *State) writeSQLFile(id TabID, doc *SQLDocument, op *sqlFileOp) {
	data, err := storage.EncodeSQL(doc.File, op.text)
	if err != nil {
		s.finishSQLFile(id, doc, op, nil, err, false)
		return
	}
	go func() {
		file, err := s.SQLServices.Files.Write(storage.SQLWriteRequest{Path: op.path, Data: data, Expected: op.expected})
		s.post(func() {
			if !s.liveSQLFileOp(id, doc, op) {
				return
			}
			var conflict *storage.FileConflict
			if errors.As(err, &conflict) {
				op.expected = conflict.Current
				op.conflict = true
				doc.FileErr = err
				return
			}
			s.finishSQLFile(id, doc, op, file, err, false)
		})
	}()
}
func (s *State) finishSQLFile(id TabID, doc *SQLDocument, op *sqlFileOp, file *storage.SQLFile, err error, canceled bool) {
	if doc.fileOp != op {
		return
	}
	delete(s.SQLServices.reservations, id)
	doc.fileOp = nil
	doc.FileErr = err
	if err != nil {
		doc.FileState = OpFailed
	} else {
		doc.FileState = OpReady
	}
	if file != nil && err == nil {
		doc.File, doc.SavedText = file, op.text
		if tab := s.Workspace.Find(id); tab != nil {
			tab.Title = filepath.Base(file.Path)
		}
		if !doc.Dirty() && op.then != nil {
			op.then()
			return
		}
	}
	if err != nil || canceled || doc.Dirty() {
		s.cancelSQLActionFor(id)
	} else {
		s.advanceSQLAction()
	}
}
func (s *State) ResolveSQLFileConflict(tab *Tab, overwrite bool) {
	if tab == nil || tab.SQL == nil {
		return
	}
	doc := &tab.SQL.Document
	op := doc.fileOp
	if op == nil || !op.conflict {
		return
	}
	if !overwrite {
		s.finishSQLFile(tab.ID, doc, op, nil, nil, true)
		return
	}
	op.conflict = false
	doc.FileErr = nil
	s.writeSQLFile(tab.ID, doc, op)
}
