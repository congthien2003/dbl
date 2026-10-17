package ui

import (
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// NativeSQLDialogs is called on workers. The parent is set before user input.
type NativeSQLDialogs struct {
	parent *mygo.Window
	dir    string
}

func NewSQLDialogs(dir string) *NativeSQLDialogs { return &NativeSQLDialogs{dir: dir} }
func (d *NativeSQLDialogs) OpenSQL() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: d.parent, Title: "Open SQL file", DefaultPath: d.dir, Filters: []mygo.FileFilter{{Name: "SQL files", Extensions: []string{"sql"}}}})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
func (d *NativeSQLDialogs) SaveSQL(suggested string) (string, error) {
	if !filepath.IsAbs(suggested) && d.dir != "" {
		if err := os.MkdirAll(d.dir, 0700); err != nil {
			return "", err
		}
		suggested = filepath.Join(d.dir, suggested)
	}
	return mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: d.parent, Title: "Save SQL file", DefaultPath: suggested, CreateDirectories: true, Filters: []mygo.FileFilter{{Name: "SQL files", Extensions: []string{"sql"}}}})
}
func (sh *Shell) SetWindow(w *mygo.Window) {
	if sh.st.SQLServices != nil {
		if d, ok := sh.st.SQLServices.Dialogs.(*NativeSQLDialogs); ok {
			d.parent = w
		}
	}
}
