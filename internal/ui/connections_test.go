package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/connection"
	"dbl/internal/database"
)

// fill focuses the control of a form field by its label and types text.
func fill(t *testing.T, tt *mygoui.Tester, label, text string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Type(text)
}

func click(t *testing.T, tt *mygoui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
}

func menuHas(menu []string, item string) bool {
	for _, m := range menu {
		if m == item {
			return true
		}
	}
	return false
}

func TestStartupMakesNoConnectionOrCredentialCalls(t *testing.T) {
	e := newEnv(t)
	e.tt.Frame()
	select {
	case <-e.calls:
		t.Error("the connector was called while drawing the explorer")
	case <-time.After(60 * time.Millisecond):
	}
	if len(e.creds.secrets) != 0 || len(e.creds.sets) != 0 {
		t.Error("the credential store was touched")
	}
}

func TestDoubleClickingAConnectionConnects(t *testing.T) {
	e := newEnv(t)
	local := e.profile("Local PG")
	doubleClickText(t, e.tt, "Local PG")
	c := e.nextCall()
	if c.profile.ID != local.ID {
		t.Fatalf("connected %q", c.profile.Name)
	}
	if !e.tt.HasText("Connecting to Local PG") {
		t.Errorf("status bar while connecting: %q", e.tt.Texts())
	}
	c.reply <- fakeReply{ad: &fakeAdapter{closed: make(chan struct{}, 1)}}
	e.runPost()
	if !e.tt.HasText("Connected: Local PG") {
		t.Errorf("status bar after connecting: %q", e.tt.Texts())
	}
	if !e.tt.HasText("Connected") { // the label of the row's dot
		t.Error("the row does not show a connected marker")
	}
}

func TestFailedConnectionShowsTheErrorAndHint(t *testing.T) {
	e := newEnv(t)
	local := e.profile("Local PG")
	doubleClickText(t, e.tt, "Local PG")
	c := e.nextCall()
	c.reply <- fakeReply{err: &database.Error{Kind: database.KindConnection, Message: "The server does not support TLS.", Hint: `Choose TLS mode "disable".`}}
	e.runPost()
	if e.sh.st.Conns.Status(local.ID) != connection.Failed {
		t.Fatalf("status %v", e.sh.st.Conns.Status(local.ID))
	}
	if !e.tt.HasText("Connection to Local PG failed: The server does not support TLS.") || !e.tt.HasText(`Choose TLS mode "disable"`) {
		t.Errorf("status bar: %q", e.tt.Texts())
	}
}

func TestConnectionMenuFollowsTheState(t *testing.T) {
	e := newEnv(t)
	if err := e.tt.RightClick("Local PG"); err != nil {
		t.Fatal(err)
	}
	menu := e.tt.Menu()
	for _, want := range []string{"Connect", "Test connection", "New SQL tab", "Edit…", "Rename…", "Move to", "Delete…"} {
		if !menuHas(menu, want) {
			t.Errorf("menu %q lacks %q", menu, want)
		}
	}
	if menuHas(menu, "Disconnect") {
		t.Error("a disconnected connection offers Disconnect")
	}
	e.tt.CloseMenu()

	e.connect("Local PG")
	if err := e.tt.RightClick("Local PG"); err != nil {
		t.Fatal(err)
	}
	menu = e.tt.Menu()
	if !menuHas(menu, "Disconnect") || menuHas(menu, "Connect") {
		t.Errorf("connected menu %q", menu)
	}
	e.tt.CloseMenu()
}

func TestMenuConnectAndCancel(t *testing.T) {
	e := newEnv(t)
	local := e.profile("Local PG")
	if err := e.tt.RightClick("Local PG"); err != nil {
		t.Fatal(err)
	}
	if err := e.tt.ChooseMenuItem("Connect"); err != nil {
		t.Fatal(err)
	}
	e.nextCall()
	if e.sh.st.Conns.Status(local.ID) != connection.Connecting {
		t.Fatal("not connecting")
	}
	if err := e.tt.RightClick("Local PG"); err != nil {
		t.Fatal(err)
	}
	if !menuHas(e.tt.Menu(), "Cancel connecting") {
		t.Errorf("menu while connecting: %q", e.tt.Menu())
	}
	if err := e.tt.ChooseMenuItem("Cancel connecting"); err != nil {
		t.Fatal(err)
	}
	if e.sh.st.Conns.Status(local.ID) != connection.Disconnected {
		t.Error("cancelling did not stop the attempt")
	}
	// The fake connector returns once its context ends; nothing is posted.
	select {
	case <-e.post:
		t.Error("a cancelled attempt posted a result")
	case <-time.After(80 * time.Millisecond):
	}
}

func TestMenuNewSQLTabIsBoundToTheConnection(t *testing.T) {
	e := newEnv(t)
	local := e.profile("Local PG")
	if err := e.tt.RightClick("Local PG"); err != nil {
		t.Fatal(err)
	}
	if err := e.tt.ChooseMenuItem("New SQL tab"); err != nil {
		t.Fatal(err)
	}
	tab := e.sh.st.Workspace.Active()
	if tab == nil || tab.Kind != app.TabSQL || tab.Table.ConnectionID != local.ID {
		t.Fatalf("active tab %+v", tab)
	}
	e.tt.Frame()
	if !e.tt.HasText("Connection: Local PG: not connected") {
		t.Errorf("SQL tab header: %q", e.tt.Texts())
	}
}

func TestMoveToSubmenu(t *testing.T) {
	e := newEnv(t)
	sandbox := e.profile("Sandbox")
	personal := e.folderItem("Personal")
	if err := e.tt.RightClick("Sandbox"); err != nil {
		t.Fatal(err)
	}
	if err := e.tt.ChooseMenuItem("Move to", "Personal"); err != nil {
		t.Fatal(err)
	}
	got, _ := e.sh.st.Catalog.Profile(sandbox.ID)
	if "folder:"+got.FolderID != personal {
		t.Errorf("folder %q", got.FolderID)
	}
	if len(e.saves) == 0 {
		t.Error("the move was not persisted")
	}
	if err := e.tt.RightClick("Sandbox"); err != nil {
		t.Fatal(err)
	}
	if err := e.tt.ChooseMenuItem("Move to", "Top level"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.sh.st.Catalog.Profile(sandbox.ID); got.FolderID != "" {
		t.Errorf("folder %q after moving to the top level", got.FolderID)
	}
}

func TestRenameConnectionDialog(t *testing.T) {
	e := newEnv(t)
	sandbox := e.profile("Sandbox")
	e.tt.RightClick("Sandbox")
	if err := e.tt.ChooseMenuItem("Rename…"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText("Rename connection") {
		t.Fatalf("no dialog; texts %q", e.tt.Texts())
	}
	// The dialog starts with the current name selected; replace it.
	fill(t, e.tt, "Name", "")
	e.tt.Key(mygoui.Cmd, mygoui.KeyA)
	e.tt.Type("Playground")
	e.tt.Key(0, mygoui.KeyEnter)
	if got, _ := e.sh.st.Catalog.Profile(sandbox.ID); got.Name != "Playground" {
		t.Errorf("name %q", got.Name)
	}
	if e.tt.HasText("Rename connection") {
		t.Error("the dialog stayed open")
	}
}

func TestRenameWithEmptyNameShowsAnError(t *testing.T) {
	e := newEnv(t)
	sandbox := e.profile("Sandbox")
	e.tt.RightClick("Sandbox")
	if err := e.tt.ChooseMenuItem("Rename…"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	fill(t, e.tt, "Name", "")
	e.tt.Key(mygoui.Cmd, mygoui.KeyA)
	e.tt.Key(0, mygoui.KeyBackspace)
	e.tt.Key(0, mygoui.KeyEnter)
	if !e.tt.HasText("Name is required") || !e.tt.HasText("Rename connection") {
		t.Errorf("texts %q", e.tt.Texts())
	}
	if got, _ := e.sh.st.Catalog.Profile(sandbox.ID); got.Name != "Sandbox" {
		t.Errorf("name %q", got.Name)
	}
}

func TestNewFolderDialog(t *testing.T) {
	e := newEmptyEnv(t)
	click(t, e.tt, "+ Folder")
	if !e.tt.HasText("New folder") {
		t.Fatalf("no dialog; texts %q", e.tt.Texts())
	}
	e.tt.Type("Clients")
	e.tt.Key(0, mygoui.KeyEnter)
	if len(e.sh.st.Catalog.Folders) != 1 || e.sh.st.Catalog.Folders[0].Name != "Clients" {
		t.Fatalf("folders %+v", e.sh.st.Catalog.Folders)
	}
	if !e.tt.HasText("Clients") {
		t.Errorf("the folder is not shown: %q", e.tt.Texts())
	}
	if len(e.saves) != 1 {
		t.Errorf("%d saves", len(e.saves))
	}
}

func TestDeleteFolderBranchesInTheUI(t *testing.T) {
	e := newEnv(t)
	work := e.sh.st.Catalog.Folders[0]
	e.tt.RightClick("Work")
	if err := e.tt.ChooseMenuItem("Delete…"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText(`Delete folder "Work"?`) || !e.tt.HasText("2 connections in it will move to the top level.") {
		t.Fatalf("dialog texts %q", e.tt.Texts())
	}
	click(t, e.tt, "Cancel")
	if _, ok := e.sh.st.Catalog.Folder(work.ID); !ok || e.tt.HasText(`Delete folder "Work"?`) {
		t.Fatal("Cancel did not keep the folder")
	}
	e.tt.RightClick("Work")
	e.tt.ChooseMenuItem("Delete…")
	e.tt.Frame()
	click(t, e.tt, "Move to top level and delete")
	if _, ok := e.sh.st.Catalog.Folder(work.ID); ok {
		t.Error("the folder is still there")
	}
	if got := e.profile("Local PG"); got.FolderID != "" {
		t.Errorf("Local PG is still in folder %q", got.FolderID)
	}
	if !e.tt.HasText("Local PG") {
		t.Error("the moved connection disappeared from the explorer")
	}
}

func TestDeleteConnectionAlwaysAsks(t *testing.T) {
	e := newEnv(t)
	local := e.profile("Local PG")
	e.connect("Local PG")
	e.sh.st.Workspace.NewSQL(local.ID)
	e.tt.Frame()
	e.tt.RightClick("Local PG")
	if err := e.tt.ChooseMenuItem("Delete…"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText(`Delete "Local PG"?`) || !e.tt.HasText("1 tab open on it will be closed.") {
		t.Fatalf("dialog texts %q", e.tt.Texts())
	}
	click(t, e.tt, "Cancel")
	if _, ok := e.sh.st.Catalog.Profile(local.ID); !ok {
		t.Fatal("Cancel deleted the connection")
	}
	e.tt.RightClick("Local PG")
	e.tt.ChooseMenuItem("Delete…")
	e.tt.Frame()
	click(t, e.tt, "Delete")
	if _, ok := e.sh.st.Catalog.Profile(local.ID); ok {
		t.Error("the connection is still there")
	}
	if len(e.sh.st.Workspace.Tabs) != 0 {
		t.Error("its tab is still open")
	}
	if e.tt.HasText("Local PG") && !e.tt.HasText("Staging PG") {
		t.Error("explorer not refreshed")
	}
}

func TestDisconnectWithTabsAsks(t *testing.T) {
	e := newEnv(t)
	local := e.profile("Local PG")
	e.connect("Local PG")
	e.sh.st.Workspace.NewSQL(local.ID)
	e.tt.Frame()
	e.tt.RightClick("Local PG")
	if err := e.tt.ChooseMenuItem("Disconnect"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText(`Disconnect "Local PG"?`) {
		t.Fatalf("no dialog; texts %q", e.tt.Texts())
	}
	click(t, e.tt, "Cancel")
	if e.sh.st.Conns.Status(local.ID) != connection.Connected || len(e.sh.st.Workspace.Tabs) != 1 {
		t.Fatal("Cancel ended the session")
	}
	e.tt.RightClick("Local PG")
	e.tt.ChooseMenuItem("Disconnect")
	e.tt.Frame()
	click(t, e.tt, "Disconnect")
	if e.sh.st.Conns.Status(local.ID) != connection.Disconnected || len(e.sh.st.Workspace.Tabs) != 0 {
		t.Error("Disconnect did not close the session and the tab")
	}
}

func TestDisconnectWithoutTabsIsImmediate(t *testing.T) {
	e := newEnv(t)
	local := e.profile("Local PG")
	e.connect("Local PG")
	e.tt.RightClick("Local PG")
	if err := e.tt.ChooseMenuItem("Disconnect"); err != nil {
		t.Fatal(err)
	}
	if e.sh.st.Conns.Status(local.ID) != connection.Disconnected || e.sh.st.PendingConn != nil {
		t.Error("a disconnect without tabs asked or did not happen")
	}
}

func TestPasswordPromptConnects(t *testing.T) {
	e := newEnv(t)
	p := e.profile("Local PG")
	p.SecretRef = connection.SecretRefFor(p.ID) // saved password that is gone
	if _, err := e.sh.st.Catalog.UpdateProfile(p); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	doubleClickText(t, e.tt, "Local PG")
	e.runPost()
	if !e.tt.HasText("Password for Local PG") {
		t.Fatalf("no prompt; texts %q", e.tt.Texts())
	}
	e.tt.Type("s3cret-pw")
	if e.tt.HasText("s3cret-pw") {
		t.Error("the typed password is shown")
	}
	e.tt.Key(0, mygoui.KeyEnter)
	c := e.nextCall()
	if c.password != "s3cret-pw" {
		t.Errorf("connected with %q", c.password)
	}
	c.reply <- fakeReply{ad: &fakeAdapter{closed: make(chan struct{}, 1)}}
	e.runPost()
	if e.tt.HasText("Password for Local PG") {
		t.Error("the prompt stayed open")
	}
	if e.sh.pw.text != "" {
		t.Error("the typed password is still held by the prompt")
	}
}

func TestPasswordPromptCancel(t *testing.T) {
	e := newEnv(t)
	p := e.profile("Local PG")
	p.SecretRef = connection.SecretRefFor(p.ID)
	e.sh.st.Catalog.UpdateProfile(p)
	e.tt.Frame()
	doubleClickText(t, e.tt, "Local PG")
	e.runPost()
	click(t, e.tt, "Cancel")
	if e.sh.st.Conns.Status(p.ID) != connection.Disconnected || e.tt.HasText("Password for Local PG") {
		t.Errorf("status %v", e.sh.st.Conns.Status(p.ID))
	}
}

// ---- The connection form ----

// openNewPicker opens the new-connection dialog on its first step, the choice
// of the database type.
func openNewPicker(t *testing.T, e *uiEnv) {
	t.Helper()
	click(t, e.tt, "+ Connection")
	if !e.tt.HasText("New connection") {
		t.Fatalf("no form; texts %q", e.tt.Texts())
	}
}

// openNewForm opens the new-connection dialog and chooses PostgreSQL.
func openNewForm(t *testing.T, e *uiEnv) {
	t.Helper()
	openNewPicker(t, e)
	click(t, e.tt, "PostgreSQL")
}

func fillValidForm(t *testing.T, e *uiEnv) {
	t.Helper()
	fill(t, e.tt, "Name", "Reporting")
	fill(t, e.tt, "Host", "db.internal")
	fill(t, e.tt, "Database", "reports")
	fill(t, e.tt, "Username", "analyst")
}

func TestNewConnectionFormSavesAProfileAndKeepsThePasswordOutOfTheFile(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	for _, want := range []string{"Name", "Folder", "Driver", "Host", "Port", "Database", "Username", "Password", "TLS", "Timeout (s)", "Test connection", "Save"} {
		if !e.tt.HasText(want) {
			t.Errorf("the form lacks %q", want)
		}
	}
	fillValidForm(t, e)
	fill(t, e.tt, "Password", "hunter2-pw")
	if e.tt.HasText("hunter2-pw") {
		t.Error("the form shows the typed password")
	}
	click(t, e.tt, "Save")
	if e.tt.HasText("New connection") {
		t.Fatalf("the form stayed open; texts %q", e.tt.Texts())
	}
	if len(e.sh.st.Catalog.Profiles) != 1 {
		t.Fatalf("%d profiles", len(e.sh.st.Catalog.Profiles))
	}
	p := e.sh.st.Catalog.Profiles[0]
	if p.Name != "Reporting" || p.Host != "db.internal" || p.Port != 5432 || p.Database != "reports" || p.Username != "analyst" {
		t.Errorf("saved %+v", p)
	}
	if p.TLS.Mode != connection.TLSRequire || p.ConnectTimeoutSec != 10 {
		t.Errorf("defaults: tls %q timeout %d", p.TLS.Mode, p.ConnectTimeoutSec)
	}
	if p.SecretRef != connection.SecretRefFor(p.ID) {
		t.Errorf("SecretRef %q", p.SecretRef)
	}
	select {
	case ref := <-e.creds.sets:
		if ref != p.SecretRef || e.creds.secrets[ref] != "hunter2-pw" {
			t.Errorf("stored %q = %q", ref, e.creds.secrets[ref])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the password was not sent to the credential store")
	}
	for _, snap := range e.saves {
		for _, q := range snap.Profiles {
			if strings.Contains(q.Name+q.Host+q.Database+q.Username+q.SecretRef, "hunter2-pw") {
				t.Error("the password reached the saved catalog")
			}
		}
	}
	if !e.tt.HasText("Reporting") {
		t.Error("the new connection is not in the explorer")
	}
}

func TestNewConnectionInTheChosenFolder(t *testing.T) {
	e := newEnv(t)
	e.tt.RightClick("Personal")
	if err := e.tt.ChooseMenuItem("New connection here…"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText("New connection") {
		t.Fatal("no form")
	}
	click(t, e.tt, "PostgreSQL")
	fillValidForm(t, e)
	click(t, e.tt, "Save")
	got := e.profile("Reporting")
	f, _ := e.sh.st.Catalog.Folder(got.FolderID)
	if f.Name != "Personal" {
		t.Errorf("created in folder %q", f.Name)
	}
}

func TestFormShowsFieldErrorsAndSavesNothing(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	click(t, e.tt, "Save")
	for _, want := range []string{"Name is required", "Host is required", "Database is required", "Username is required"} {
		if !e.tt.HasText(want) {
			t.Errorf("no error %q; texts %q", want, e.tt.Texts())
		}
	}
	if !e.tt.HasText("New connection") {
		t.Error("the form closed although it is invalid")
	}
	if len(e.sh.st.Catalog.Profiles) != 0 || len(e.saves) != 0 {
		t.Error("an invalid form saved something")
	}

	fillValidForm(t, e)
	fill(t, e.tt, "Port", "abc")
	click(t, e.tt, "Save")
	if !e.tt.HasText("Port must be a number") || len(e.sh.st.Catalog.Profiles) != 0 {
		t.Errorf("texts %q", e.tt.Texts())
	}
}

func TestFormCancelAndEscapeDiscard(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	fillValidForm(t, e)
	click(t, e.tt, "Cancel")
	if e.tt.HasText("New connection") || len(e.sh.st.Catalog.Profiles) != 0 {
		t.Error("Cancel kept or saved the form")
	}
	openNewForm(t, e)
	if e.tt.HasText("Reporting") {
		t.Error("a reopened form kept the old values")
	}
	e.tt.Key(0, mygoui.KeyEscape)
	if e.tt.HasText("New connection") {
		t.Error("Escape did not close the form")
	}
}

func TestEditFormIsPrefilledWithoutThePassword(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	fillValidForm(t, e)
	fill(t, e.tt, "Password", "stored-pw")
	click(t, e.tt, "Save")
	<-e.creds.sets
	e.runPost()
	e.sh.pw = pwPrompt{}

	e.tt.RightClick("Reporting")
	if err := e.tt.ChooseMenuItem("Edit…"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText("Edit connection") {
		t.Fatalf("no edit form; texts %q", e.tt.Texts())
	}
	f := e.sh.form
	if f == nil || f.name != "Reporting" || f.host != "db.internal" || f.database != "reports" || f.user != "analyst" {
		t.Fatalf("form %+v", f)
	}
	if f.password != "" || !f.hasSecret {
		t.Errorf("password %q hasSecret %v", f.password, f.hasSecret)
	}
	if e.tt.HasText("stored-pw") {
		t.Error("the saved password is shown")
	}
	if !e.tt.HasText("Remove saved password") {
		t.Error("no way to remove the saved password")
	}
	if e.creds.secrets["dbl/"+e.profile("Reporting").ID] != "stored-pw" {
		t.Error("the secret changed")
	}
}

func TestEditFormHasNoRemoveBoxWithoutASavedPassword(t *testing.T) {
	e := newEnv(t)
	e.tt.RightClick("Sandbox")
	e.tt.ChooseMenuItem("Edit…")
	e.tt.Frame()
	if !e.tt.HasText("Edit connection") {
		t.Fatal("no form")
	}
	if e.tt.HasText("Remove saved password") {
		t.Error("the box is offered although no password is saved")
	}
}

func TestEditSavesChangesAndKeepsThePassword(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	fillValidForm(t, e)
	fill(t, e.tt, "Password", "stored-pw")
	click(t, e.tt, "Save")
	<-e.creds.sets
	e.runPost()
	before := e.profile("Reporting")

	e.tt.RightClick("Reporting")
	e.tt.ChooseMenuItem("Edit…")
	e.tt.Frame()
	fill(t, e.tt, "Host", "")
	e.tt.Key(mygoui.Cmd, mygoui.KeyA)
	e.tt.Type("other.internal")
	click(t, e.tt, "Save")
	after := e.profile("Reporting")
	if after.Host != "other.internal" || after.SecretRef != before.SecretRef || after.ID != before.ID {
		t.Errorf("before %+v after %+v", before, after)
	}
	if e.creds.secrets[before.SecretRef] != "stored-pw" {
		t.Error("an edit without a new password changed the secret")
	}
}

func TestEditingAConnectedProfileAsksBeforeApplying(t *testing.T) {
	e := newEnv(t)
	local := e.profile("Local PG")
	e.connect("Local PG")
	e.tt.RightClick("Local PG")
	e.tt.ChooseMenuItem("Edit…")
	e.tt.Frame()
	fill(t, e.tt, "Database", "")
	e.tt.Key(mygoui.Cmd, mygoui.KeyA)
	e.tt.Type("other")
	click(t, e.tt, "Save")
	if !e.tt.HasText(`Save changes to "Local PG"?`) {
		t.Fatalf("no confirmation; texts %q", e.tt.Texts())
	}
	if got, _ := e.sh.st.Catalog.Profile(local.ID); got.Database != "app" {
		t.Fatal("applied before the user answered")
	}
	click(t, e.tt, "Save and disconnect")
	got, _ := e.sh.st.Catalog.Profile(local.ID)
	if got.Database != "other" || e.sh.st.Conns.Status(local.ID) != connection.Disconnected {
		t.Errorf("database %q status %v", got.Database, e.sh.st.Conns.Status(local.ID))
	}
}

func TestFormTestConnection(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	fillValidForm(t, e)
	fill(t, e.tt, "Password", "form-pw")
	click(t, e.tt, "Test connection")
	c := e.nextCall()
	if c.password != "form-pw" || c.profile.Host != "db.internal" {
		t.Errorf("test used %q / %q", c.password, c.profile.Host)
	}
	if !e.tt.HasText("Testing the connection") || !e.tt.HasText("Cancel test") {
		t.Errorf("no progress; texts %q", e.tt.Texts())
	}
	c.reply <- fakeReply{ad: &fakeAdapter{closed: make(chan struct{}, 1)}}
	e.runPost()
	if !e.tt.HasText("Connection succeeded") {
		t.Errorf("no result; texts %q", e.tt.Texts())
	}
	if len(e.sh.st.Catalog.Profiles) != 0 {
		t.Error("testing saved the profile")
	}
	if len(e.sh.st.Conns.Active()) != 0 {
		t.Error("testing registered a connection")
	}

	click(t, e.tt, "Test connection")
	c = e.nextCall()
	c.reply <- fakeReply{err: &database.Error{Kind: database.KindAuthentication, Message: "Authentication failed.", Hint: "Check the username and password."}}
	e.runPost()
	if !e.tt.HasText("Authentication failed. Check the username and password.") {
		t.Errorf("no failure; texts %q", e.tt.Texts())
	}
}

func TestFormTestCanBeCancelledAndClosingTheFormCancelsIt(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	fillValidForm(t, e)
	click(t, e.tt, "Test connection")
	e.nextCall()
	click(t, e.tt, "Cancel test")
	if e.sh.form.testing || e.tt.HasText("Cancel test") {
		t.Error("the test is still shown as running")
	}
	click(t, e.tt, "Test connection")
	c := e.nextCall()
	click(t, e.tt, "Cancel")
	if e.sh.form != nil {
		t.Fatal("the form is still open")
	}
	// A result that arrives for a closed form is dropped without a panic.
	c.reply <- fakeReply{err: errors.New("late")}
	time.Sleep(60 * time.Millisecond)
	select {
	case fn := <-e.post:
		fn()
	default:
	}
}

func TestFormTestWithInvalidFieldsDoesNotConnect(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	click(t, e.tt, "Test connection")
	if !e.tt.HasText("Host is required") {
		t.Errorf("texts %q", e.tt.Texts())
	}
	select {
	case <-e.calls:
		t.Error("the connector was called for an invalid form")
	case <-time.After(60 * time.Millisecond):
	}
}

func TestReadOnlyCatalogDisablesEditing(t *testing.T) {
	e := newEnv(t)
	e.sh.st.ConnectionsReadOnly = "connections.json was written by a newer version"
	e.tt.Frame()
	click(t, e.tt, "+ Connection")
	click(t, e.tt, "+ Folder")
	if e.sh.form != nil || e.sh.nameDlg != nil {
		t.Error("a dialog opened although connections are read-only")
	}
	e.tt.RightClick("Local PG")
	if err := e.tt.ChooseMenuItem("Delete…"); err == nil {
		t.Error("Delete is enabled while read-only")
	}
	if err := e.tt.ChooseMenuItem("Edit…"); err == nil {
		t.Error("Edit is enabled while read-only")
	}
	e.tt.CloseMenu()
	// Connecting still works.
	e.tt.RightClick("Local PG")
	if err := e.tt.ChooseMenuItem("Connect"); err != nil {
		t.Errorf("Connect is blocked while read-only: %v", err)
	}
}

func TestMenuTestConnectionShowsTheOutcomeInTheStatusBar(t *testing.T) {
	e := newEnv(t)
	e.tt.RightClick("Local PG")
	if err := e.tt.ChooseMenuItem("Test connection"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText(`Testing "Local PG"`) {
		t.Errorf("texts %q", e.tt.Texts())
	}
	c := e.nextCall()
	c.reply <- fakeReply{ad: &fakeAdapter{closed: make(chan struct{}, 1)}}
	e.runPost()
	if !e.tt.HasText(`Test of "Local PG" succeeded`) {
		t.Errorf("texts %q", e.tt.Texts())
	}
}

// The window can be as small as 800x500; the buttons of the form must stay
// on screen, also for the longest form.
func TestFormFitsTheSmallestWindow(t *testing.T) {
	e := newEmptyEnv(t)
	e.tt.SetSize(800, 500)
	e.sh.st.Settings.Window.Width, e.sh.st.Settings.Window.Height = 800, 500
	e.tt.Frame()
	openNewForm(t, e)
	e.sh.form.tls = "verify-full" // the longest form: the CA certificate row too
	e.sh.form.result, e.sh.form.resultOK = "Connection succeeded (12 ms).", true
	e.tt.Frame()
	for _, label := range []string{"Save", "Cancel", "Test connection", "Connection succeeded (12 ms)."} {
		r, ok := e.tt.Find(label)
		if !ok {
			t.Fatalf("no %q on screen", label)
		}
		if r.W == 0 || r.H == 0 || r.Y < 0 || r.Y+r.H > 500 || r.X < 0 || r.X+r.W > 800 {
			t.Errorf("%q at %+v is outside the 800x500 window", label, r)
		}
	}
	// The fields still reach every control by scrolling; typing into a field
	// that is on screen works.
	fill(t, e.tt, "Name", "Reporting")
	if e.sh.form.name != "Reporting" {
		t.Errorf("name %q", e.sh.form.name)
	}
}

func TestFormUsesTheHeightOfALargeWindow(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	for _, label := range []string{"Timeout (s)", "Save"} {
		r, ok := e.tt.Find(label)
		if !ok || r.Y+r.H > 800 || (label == "Save" && r.W == 0) {
			t.Errorf("%q at %+v in the 1280x800 window", label, r)
		}
	}
}

// ---- The choice of the database type ----

func TestNewConnectionStartsWithPicker(t *testing.T) {
	e := newEmptyEnv(t)
	openNewPicker(t, e)
	if e.sh.form == nil || e.sh.form.step != stepPickDriver {
		t.Fatalf("form %+v", e.sh.form)
	}
	for _, field := range []string{"Host", "Port", "Username", "Password", "Test connection", "Save"} {
		if e.tt.HasText(field) {
			t.Errorf("the picker shows the field %q", field)
		}
	}
}

func TestPickingPostgresOpensTheFormWithDefaults(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	f := e.sh.form
	if f.step != stepFields || f.driver != connection.DriverPostgres {
		t.Fatalf("step %v driver %q", f.step, f.driver)
	}
	if f.port != "5432" || f.tls != string(connection.DefaultTLSMode) || f.timeout != "10" {
		t.Errorf("defaults: port %q tls %q timeout %q", f.port, f.tls, f.timeout)
	}
	for _, want := range []string{"Host", "Port", "Back", "Save"} {
		if !e.tt.HasText(want) {
			t.Errorf("the form lacks %q", want)
		}
	}
}

func TestEditSkipsPickerAndLocksDriver(t *testing.T) {
	e := newEnv(t)
	e.tt.RightClick("Local PG")
	if err := e.tt.ChooseMenuItem("Edit…"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	f := e.sh.form
	if f == nil || f.step != stepFields || f.driver != connection.DriverPostgres {
		t.Fatalf("form %+v", f)
	}
	if e.tt.HasText("Choose a database type") || e.tt.HasText("Back") {
		t.Errorf("an edit shows the picker or Back; texts %q", e.tt.Texts())
	}
	if !e.tt.HasText("PostgreSQL") {
		t.Error("the form does not name the driver")
	}
}

func TestBackReturnsToPickerKeepingInput(t *testing.T) {
	e := newEmptyEnv(t)
	openNewForm(t, e)
	fillValidForm(t, e)
	click(t, e.tt, "Back")
	if e.sh.form.step != stepPickDriver || !e.tt.HasText("Choose a database type") {
		t.Fatalf("step %v; texts %q", e.sh.form.step, e.tt.Texts())
	}
	click(t, e.tt, "PostgreSQL")
	f := e.sh.form
	if f.step != stepFields || f.name != "Reporting" || f.host != "db.internal" || f.database != "reports" {
		t.Errorf("input was lost: %+v", f)
	}
}

// ---- The SQLite form ----

func openNewSQLiteForm(t *testing.T, e *uiEnv) {
	t.Helper()
	openNewPicker(t, e)
	click(t, e.tt, "SQLite")
	if e.sh.form.driver != connection.DriverSQLite || e.sh.form.step != stepFields {
		t.Fatalf("form %+v", e.sh.form)
	}
}

// sqliteFile creates an empty file that passes the check of the form.
func sqliteFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSQLiteFormShowsOnlyFileFields(t *testing.T) {
	e := newEmptyEnv(t)
	openNewSQLiteForm(t, e)
	for _, want := range []string{"Name", "Folder", "File", "Browse…", "Open read-only", "Test connection", "Save"} {
		if !e.tt.HasText(want) {
			t.Errorf("the form lacks %q; texts %q", want, e.tt.Texts())
		}
	}
	for _, not := range []string{"Host", "Port", "Username", "Password", "TLS", "Timeout (s)", "Database"} {
		if e.tt.HasText(not) {
			t.Errorf("the SQLite form shows %q", not)
		}
	}
}

func TestSQLiteFormSaveBuildsProfile(t *testing.T) {
	e := newEmptyEnv(t)
	openNewSQLiteForm(t, e)
	path := sqliteFile(t)
	fill(t, e.tt, "Name", "Notes")
	e.sh.form.path = path
	click(t, e.tt, "Open read-only")
	click(t, e.tt, "Save")
	if e.tt.HasText("New connection") {
		t.Fatalf("the form stayed open; texts %q", e.tt.Texts())
	}
	p := e.profile("Notes")
	if p.Driver != connection.DriverSQLite || p.SQLitePath != path || !p.ReadOnly || p.Host != "" || p.SecretRef != "" {
		t.Errorf("saved %+v", p)
	}
}

func TestSQLiteFormMissingFileShowsError(t *testing.T) {
	e := newEmptyEnv(t)
	openNewSQLiteForm(t, e)
	fill(t, e.tt, "Name", "Notes")
	click(t, e.tt, "Save")
	if !e.tt.HasText("File is required") || len(e.sh.st.Catalog.Profiles) != 0 {
		t.Errorf("texts %q", e.tt.Texts())
	}
	e.sh.form.path = filepath.Join(t.TempDir(), "absent.db")
	click(t, e.tt, "Save")
	if !e.tt.HasText("File not found") || len(e.sh.st.Catalog.Profiles) != 0 {
		t.Errorf("texts %q", e.tt.Texts())
	}
}

func TestSQLiteTestButtonUsesManagerTest(t *testing.T) {
	e := newEmptyEnv(t)
	openNewSQLiteForm(t, e)
	path := sqliteFile(t)
	fill(t, e.tt, "Name", "Notes")
	e.sh.form.path = path
	click(t, e.tt, "Test connection")
	c := e.nextCall()
	if c.profile.Driver != connection.DriverSQLite || c.profile.SQLitePath != path || c.password != "" {
		t.Errorf("tested %+v with password %q", c.profile, c.password)
	}
	c.reply <- fakeReply{ad: &fakeAdapter{closed: make(chan struct{}, 1)}}
	e.runPost()
	if !e.tt.HasText("Connection succeeded") && !strings.Contains(e.sh.form.result, "Connection succeeded") {
		t.Errorf("result %q", e.sh.form.result)
	}
}

func TestBrowseSetsPathOnlyWhileFormOpen(t *testing.T) {
	e := newEmptyEnv(t)
	release := make(chan string)
	e.sh.pickFile = func() (string, error) { return <-release, nil }

	openNewSQLiteForm(t, e)
	click(t, e.tt, "Browse…")
	release <- "chosen.db"
	e.runPost()
	if e.sh.form.path != "chosen.db" || e.sh.form.browsing {
		t.Errorf("path %q browsing %v", e.sh.form.path, e.sh.form.browsing)
	}

	// The form closes while the dialog is open: the answer is dropped.
	f := e.sh.form
	click(t, e.tt, "Browse…")
	click(t, e.tt, "Cancel")
	release <- "late.db"
	e.runPost()
	if e.sh.form != nil || f.path != "chosen.db" {
		t.Errorf("form %v path %q", e.sh.form, f.path)
	}
}

func TestEditSQLiteLoadsPathAndReadOnly(t *testing.T) {
	e := newEmptyEnv(t)
	path := sqliteFile(t)
	if _, err := e.sh.st.Catalog.AddProfile(connection.Profile{Name: "Notes", Driver: connection.DriverSQLite, SQLitePath: path, ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	e.tt.RightClick("Notes")
	if err := e.tt.ChooseMenuItem("Edit…"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	f := e.sh.form
	if f == nil || f.driver != connection.DriverSQLite || f.step != stepFields || f.path != path || !f.readOnly {
		t.Fatalf("form %+v", f)
	}
	if e.tt.HasText("Host") || !e.tt.HasText("File") {
		t.Errorf("texts %q", e.tt.Texts())
	}
	click(t, e.tt, "Save")
	if got := e.profile("Notes"); got.SQLitePath != path || !got.ReadOnly || got.Driver != connection.DriverSQLite {
		t.Errorf("saved %+v", got)
	}
}
