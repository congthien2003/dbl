package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/connection"
)

// tlsLabels are the TLS modes as the form offers them.
var tlsLabels = []string{
	string(connection.TLSDisable), string(connection.TLSRequire),
	string(connection.TLSVerifyCA), string(connection.TLSVerifyFull),
}

func tlsHint(mode string) string {
	switch connection.TLSMode(mode) {
	case connection.TLSDisable:
		return "No encryption. Use it only for a server you reach locally."
	case connection.TLSRequire:
		return "Encrypted; the server certificate is not checked."
	case connection.TLSVerifyCA:
		return "Encrypted; the certificate must be signed by the CA."
	case connection.TLSVerifyFull:
		return "Encrypted; the certificate and the host name are checked."
	}
	return ""
}

const topLevel = "(top level)"

// formChrome is the height of the window the connection dialog needs besides
// its scrolling fields: the 80 DIPs the dialog stays short of the window,
// the padding, the title, the gaps, a result line and the buttons.
const formChrome = 231

// connForm is the create/edit connection dialog. It holds what the user
// typed as text; nothing reaches the catalog until Save succeeds. It never
// holds the saved password: the field starts empty on every edit.
type connForm struct {
	editingID string // "" for a new connection
	existing  connection.Profile
	hasSecret bool // the connection has a saved password

	name, host, port, database, user string
	password, rootCert, timeout      string
	folder, tls                      string // the option shown
	remove                           bool   // "Remove saved password"

	folderLabels []string // index-aligned with folderIDs; [0] is the top level
	folderIDs    []string

	errs    map[string]string
	general string

	testing    bool
	cancelTest func()
	result     string
	resultOK   bool
}

// openConnForm opens the dialog to create a connection in folderID, or, with
// editID, to edit that connection.
func (sh *Shell) openConnForm(editID, folderID string) {
	f := &connForm{errs: map[string]string{}}
	f.folderLabels, f.folderIDs = []string{topLevel}, []string{""}
	seen := map[string]int{}
	for _, fo := range sh.st.Catalog.Folders {
		label := fo.Name
		if seen[fo.Name]++; seen[fo.Name] > 1 {
			label = fmt.Sprintf("%s (%d)", fo.Name, seen[fo.Name])
		}
		f.folderLabels = append(f.folderLabels, label)
		f.folderIDs = append(f.folderIDs, fo.ID)
	}
	if editID == "" {
		f.port = strconv.Itoa(connection.DefaultPort)
		f.timeout = strconv.Itoa(connection.DefaultConnectTimeoutSec)
		f.tls = string(connection.DefaultTLSMode)
		f.setFolder(folderID)
		sh.form = f
		return
	}
	p, ok := sh.st.Catalog.Profile(editID)
	if !ok {
		return
	}
	f.editingID, f.existing, f.hasSecret = editID, p, p.SecretRef != ""
	f.name, f.host, f.port = p.Name, p.Host, strconv.Itoa(p.Port)
	f.database, f.user = p.Database, p.Username
	f.timeout = strconv.Itoa(p.ConnectTimeoutSec)
	f.tls, f.rootCert = string(p.TLS.Mode), p.TLS.RootCert
	f.setFolder(p.FolderID)
	sh.form = f
}

func (f *connForm) setFolder(id string) {
	f.folder = f.folderLabels[0]
	for i, fid := range f.folderIDs {
		if fid == id {
			f.folder = f.folderLabels[i]
		}
	}
}

func (f *connForm) folderID() string {
	for i, l := range f.folderLabels {
		if l == f.folder {
			return f.folderIDs[i]
		}
	}
	return ""
}

// profile builds the profile from the fields. Fields that are not numbers
// are reported in errs.
func (f *connForm) profile() (connection.Profile, connection.PasswordChange, map[string]string) {
	errs := map[string]string{}
	port, err := strconv.Atoi(strings.TrimSpace(f.port))
	if err != nil {
		errs[connection.FieldPort] = "Port must be a number"
	}
	timeout, err := strconv.Atoi(strings.TrimSpace(f.timeout))
	if err != nil {
		errs[connection.FieldTimeout] = "Timeout must be a number of seconds"
	}
	p := connection.Profile{
		ID: f.editingID, Name: f.name, FolderID: f.folderID(),
		Driver: connection.DriverPostgres,
		Host:   f.host, Port: port, Database: f.database, Username: f.user,
		TLS:               connection.TLSOptions{Mode: connection.TLSMode(f.tls)},
		ConnectTimeoutSec: timeout,
		Pool:              f.existing.Pool,
	}
	if f.editingID != "" {
		p.Driver = f.existing.Driver
	}
	if p.TLS.Mode.Verifies() {
		p.TLS.RootCert = f.rootCert
	}
	var change connection.PasswordChange
	switch {
	case f.password != "":
		change = connection.PasswordChange{Mode: connection.SetPassword, Value: f.password}
	case f.remove && f.hasSecret:
		change = connection.PasswordChange{Mode: connection.RemovePassword}
	}
	return p, change, errs
}

// problems checks the form the way Save does, without saving.
func (f *connForm) problems() (connection.Profile, connection.PasswordChange, map[string]string) {
	p, change, errs := f.profile()
	if len(errs) > 0 {
		return p, change, errs
	}
	p = p.Normalized()
	all := append(p.Validate(), p.ValidateFiles()...)
	if change.Mode == connection.SetPassword {
		if fe := connection.ValidatePassword(change.Value); fe != nil {
			all = append(all, *fe)
		}
	}
	for _, fe := range all {
		if _, dup := errs[fe.Field]; !dup {
			errs[fe.Field] = fe.Message
		}
	}
	return p, change, errs
}

func (sh *Shell) closeConnForm() {
	if f := sh.form; f != nil && f.cancelTest != nil {
		f.cancelTest()
	}
	sh.form = nil
}

// save validates and saves the form. The form closes when the connection
// was saved, or when saving waits for the user to agree to end a session.
func (sh *Shell) saveConnForm(f *connForm) {
	f.general = ""
	p, change, errs := f.problems()
	if len(errs) == 0 {
		_, err := sh.st.SaveProfile(p, change)
		var ve *connection.ValidationError
		switch {
		case err == nil:
			if p.FolderID != "" {
				sh.explorer.state.Open.Add(folderItem(p.FolderID))
			}
			sh.closeConnForm()
			return
		case errors.As(err, &ve):
			for _, fe := range ve.Fields {
				if _, dup := errs[fe.Field]; !dup {
					errs[fe.Field] = fe.Message
				}
			}
		default:
			f.general = err.Error()
		}
	}
	f.errs = errs
}

// testConnForm starts a connection test with the form's values.
func (sh *Shell) testConnForm(f *connForm) {
	f.result = ""
	p, change, errs := f.problems()
	f.errs = errs
	if len(errs) > 0 {
		return
	}
	if sh.st.Conns == nil {
		f.result, f.resultOK = "Connections are not available.", false
		return
	}
	f.testing = true
	f.cancelTest = sh.st.Conns.Test(connection.TestRequest{Profile: p, ExistingID: f.editingID, Password: change}, func(res connection.TestResult) {
		f.testing, f.cancelTest = false, nil
		switch {
		case res.OK:
			f.result, f.resultOK = fmt.Sprintf("Connection succeeded (%d ms).", res.Duration.Milliseconds()), true
		case res.NeedsPassword:
			f.result, f.resultOK = "No saved password was found. Type the password to test with.", false
		case res.Err != nil:
			f.result, f.resultOK = res.Err.Message, false
			if res.Err.Hint != "" {
				f.result += " " + res.Err.Hint
			}
		}
	})
}

func (sh *Shell) connFormView(c *mygoui.Context, t *mygoui.Theme) {
	f := sh.form
	if f == nil {
		return
	}
	title := "New connection"
	if f.editingID != "" {
		title = "Edit connection"
	}
	open := true
	var save, test, cancelTest bool
	// The dialog is at most 80 DIPs shorter than the window; its title,
	// padding, gaps, result line and buttons take the rest, and the fields
	// scroll in what remains.
	scrollMax := min(max(float32(sh.st.Settings.Window.Height)-formChrome, 160), 560)
	mygoui.Modal(c, &open, func() {
		mygoui.Column(c).Width(560).Padding(20).Gap(12).Children(func() {
			mygoui.Text(c, title).FontSize(16).Bold()
			mygoui.Scroll(c).MaxHeight(scrollMax).Children(func() {
				mygoui.Form(c, func() {
					submitOn := func(e mygoui.Element) {
						if e.Submitted() {
							save = true
						}
					}
					mygoui.Field(c, "Name", func() {
						submitOn(mygoui.TextInput(c, &f.name).AutoFocus().Placeholder("Local PostgreSQL"))
					}).Error(f.errs[connection.FieldName])
					mygoui.Field(c, "Folder", func() {
						mygoui.Select(c, &f.folder, f.folderLabels)
					})
					mygoui.Field(c, "Driver", func() {
						driver := connection.DriverPostgres.Label()
						mygoui.Select(c, &driver, []string{driver}).Disabled(true)
					}).Error(f.errs[connection.FieldDriver])
					mygoui.Field(c, "Host", func() {
						submitOn(mygoui.TextInput(c, &f.host).Placeholder("localhost"))
					}).Error(f.errs[connection.FieldHost])
					mygoui.Field(c, "Port", func() {
						submitOn(mygoui.TextInput(c, &f.port).Placeholder("5432"))
					}).Error(f.errs[connection.FieldPort])
					mygoui.Field(c, "Database", func() {
						submitOn(mygoui.TextInput(c, &f.database).Placeholder("postgres"))
					}).Error(f.errs[connection.FieldDatabase])
					mygoui.Field(c, "Username", func() {
						submitOn(mygoui.TextInput(c, &f.user).Placeholder("postgres"))
					}).Error(f.errs[connection.FieldUsername])
					mygoui.Field(c, "Password", func() {
						submitOn(mygoui.TextInput(c, &f.password).Password().Placeholder(passwordPlaceholder(f)))
					}).Description(passwordHint(f)).Error(f.errs[connection.FieldPassword])
					if f.hasSecret {
						mygoui.Field(c, "", func() {
							mygoui.Checkbox(c, &f.remove, "Remove saved password").Disabled(f.password != "")
						})
					}
					mygoui.Field(c, "TLS", func() {
						mygoui.Select(c, &f.tls, tlsLabels)
					}).Description(tlsHint(f.tls)).Error(f.errs[connection.FieldTLS])
					if connection.TLSMode(f.tls).Verifies() {
						mygoui.Field(c, "CA certificate", func() {
							submitOn(mygoui.TextInput(c, &f.rootCert).Placeholder("Path to a PEM file (optional)"))
						}).Error(f.errs[connection.FieldRootCert])
					}
					mygoui.Field(c, "Timeout (s)", func() {
						submitOn(mygoui.TextInput(c, &f.timeout).Placeholder("10"))
					}).Error(f.errs[connection.FieldTimeout])
				}).FillWidth()
			}).FillWidth()
			if f.general != "" {
				mygoui.Text(c, f.general).TextColor(t.Danger)
			}
			if f.testing {
				mygoui.Row(c).Gap(8).AlignItems(mygoui.Center).Children(func() {
					mygoui.Spinner(c)
					mygoui.Text(c, "Testing the connection…").TextColor(t.TextMuted)
				})
			} else if f.result != "" {
				color := t.Danger
				if f.resultOK {
					color = t.Success
				}
				mygoui.Text(c, f.result).TextColor(color)
			}
			mygoui.Row(c).Gap(8).AlignItems(mygoui.Center).Children(func() {
				if f.testing {
					if mygoui.Button(c, "Cancel test").Clicked() {
						cancelTest = true
					}
				} else if mygoui.Button(c, "Test connection").Clicked() {
					test = true
				}
				mygoui.Box(c).Grow(1)
				if mygoui.Button(c, "Cancel").Clicked() {
					open = false
				}
				if mygoui.PrimaryButton(c, "Save").Clicked() {
					save = true
				}
			})
		})
	})
	switch {
	case !open:
		sh.closeConnForm()
		c.Invalidate()
	case cancelTest:
		if f.cancelTest != nil {
			f.cancelTest()
		}
		f.testing, f.cancelTest = false, nil
		c.Invalidate()
	case test:
		sh.testConnForm(f)
		c.Invalidate()
	case save:
		sh.saveConnForm(f)
		c.Invalidate()
	}
}

func passwordPlaceholder(f *connForm) string {
	if f.hasSecret {
		return "Leave empty to keep the saved password"
	}
	return ""
}

func passwordHint(f *connForm) string {
	if f.hasSecret {
		return "A password is saved. Type a new one to replace it."
	}
	return "Saved in the system credential store, never in connections.json."
}
