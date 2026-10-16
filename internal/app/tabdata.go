package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// pageCacheSize is how many pages of a table tab are kept: the page in view
// and the last ones visited, so turning back is instant. Older pages are
// dropped, least recently used first. Reload, sort, filter and a metadata
// refresh that changes the layout empty the cache, so a cached page is never
// older than the last of those.
const pageCacheSize = 4

// Page is one page of a table, as read.
type Page struct {
	Rows [][]grid.Value
	// Keys holds the canonical identity of each row (the text of its key
	// columns), or nil when the table has no verified identity.
	Keys []string
	// IDs holds the stable grid row number of each row (see TableData.RowID),
	// or nil together with Keys.
	IDs    []int64
	Number int // 1 for the first page
	// Req is the request that produced the page; reloading repeats it.
	Req              database.PageRequest
	HasPrev, HasNext bool
}

// Start is the 1-based number of the first row of the page, counted from
// the start of the walk (exact for the pages reached by next and previous).
func (p *Page) Start(size int) int { return (p.Number-1)*size + 1 }

// TableData is the state of a table tab that reads from a database (spec
// 7.1): the metadata, the sort and filter, the page in view and the state
// of the reads. All of it changes on the UI thread only. It is the grid's
// grid.Source.
type TableData struct {
	Key  TableKey
	Info database.Table // as the explorer listed it

	Meta      *database.TableMetadata
	MetaState OpState
	MetaErr   *database.Error // of the last read; the metadata read before stays

	// Plan is how the table is ordered and paged for the current Sort.
	Plan   database.QueryPlan
	Sort   *database.Sort
	Filter *database.Filter
	Size   int

	// Page is the page in view. While a read runs, or after one failed, it is
	// the last page that was read, which stays visible.
	Page      *Page
	PageState OpState
	PageErr   *database.Error
	// PageSeq changes whenever Page is replaced, so the view can drop what
	// belonged to the old rows (the selection).
	PageSeq int

	// Editing (Phase 2.3). Edits live in Tab.Drafts, keyed by the stable row
	// number; snapshots hold, for each row that has a draft, the row as it was
	// loaded when the first edit was made. A Save is built from the snapshots
	// and the drafts only, never from Page, so replacing the page can never
	// send an edit to another row.
	snapshots map[int64]*RowSnapshot
	rowIDs    map[string]int64
	lastRowID int64

	SaveState OpState
	// SaveErr is the error of the last Save; its Outcome says whether the
	// database is known to be unchanged.
	SaveErr *database.SaveError
	// RowErrors holds, by row number, why a row could not be saved.
	RowErrors map[int64]string
	// Stale is set once a Save is committed and cleared when a page is read
	// again: until then the rows shown are older than the save, so they
	// cannot be edited.
	Stale bool

	cols   []grid.Column
	cache  *grid.Cache[*Page]
	metaOp opSlot
	pageOp opSlot
	saveOp opSlot
}

// RowSnapshot is a row as it was loaded: its identity and every value. It is
// taken when the row gets its first draft.
type RowSnapshot struct {
	Key    []database.KeyValue
	Values []grid.Value
}

func newTableData(key TableKey, info database.Table) *TableData {
	return &TableData{
		Key: key, Info: info, Size: database.DefaultPageSize,
		cache: grid.NewCache[*Page](pageCacheSize),
	}
}

// release stops the reads of the tab and drops what it holds.
func (d *TableData) release() {
	if d == nil {
		return
	}
	d.metaOp.stop()
	d.pageOp.stop()
	d.saveOp.stop()
	d.cache.Clear()
	d.Page = nil
	d.snapshots = nil
}

// Loading reports whether a read is running.
func (d *TableData) Loading() bool {
	return d.MetaState == OpLoading || d.PageState == OpLoading
}

// ---- grid.Source ----

var _ grid.Source = (*TableData)(nil)

// Len implements grid.Source.
func (d *TableData) Len() int {
	if d.Page == nil {
		return 0
	}
	return len(d.Page.Rows)
}

// Columns implements grid.Source.
func (d *TableData) Columns() []grid.Column { return d.cols }

// Value implements grid.Source. A cell outside the page is NULL.
func (d *TableData) Value(row, col int) grid.Value {
	if d.Page == nil || row < 0 || row >= len(d.Page.Rows) || col < 0 || col >= len(d.Page.Rows[row]) {
		return grid.Value{Kind: grid.KindNull}
	}
	return d.Page.Rows[row][col]
}

// RowID implements grid.Source. For a table with a verified identity it is a
// number given to the row's key for the life of the tab, so the same row has
// the same number on every page and after every reload, and two rows never
// share one. Without an identity nothing can be edited, and it is the
// position in the page.
func (d *TableData) RowID(row int) int64 {
	if d.Page != nil && row >= 0 && row < len(d.Page.IDs) {
		return d.Page.IDs[row]
	}
	return int64(row) + 1
}

// intern returns the number of a row key.
func (d *TableData) intern(key string) int64 {
	if id, ok := d.rowIDs[key]; ok {
		return id
	}
	if d.rowIDs == nil {
		d.rowIDs = map[string]int64{}
	}
	d.lastRowID++
	d.rowIDs[key] = d.lastRowID
	return d.lastRowID
}

// EditLock says why the cells cannot be edited at this moment, or "" when
// they can.
func (d *TableData) EditLock() string {
	switch {
	case d.Meta == nil:
		return "The table is not read yet."
	case !d.Meta.HasEditableColumn():
		_, why := d.Meta.Editable()
		if why == "" {
			why = "No column of this table can be edited."
		}
		return why
	case d.SaveState == OpLoading:
		return "Saving…"
	case d.Stale:
		return "The changes were saved, but the table was not read again. Reload it to edit."
	case d.Loading():
		return "Loading…"
	}
	return ""
}

type pageRow struct{ page, row int }

// RowKey implements grid.Source: the key of the row when the table has a
// verified identity, else the position in the page.
func (d *TableData) RowKey(row int) any {
	if d.Page != nil && row >= 0 && row < len(d.Page.Keys) {
		return d.Page.Keys[row]
	}
	n := 0
	if d.Page != nil {
		n = d.Page.Number
	}
	return pageRow{n, row}
}

// ReadOnly implements grid.Source: nothing in the table can be edited.
func (d *TableData) ReadOnly() bool { return d.Meta == nil || !d.Meta.HasEditableColumn() }

func columnWidth(c database.ColumnClass) float32 {
	switch c {
	case database.ClassNumber:
		return 110
	case database.ClassBool:
		return 90
	case database.ClassDateTime:
		return 190
	case database.ClassJSON:
		return 220
	case database.ClassBinary:
		return 130
	}
	return 180
}

func buildColumns(m *database.TableMetadata) []grid.Column {
	cols := make([]grid.Column, len(m.Columns))
	for i, c := range m.Columns {
		ok, why := m.CanEditColumn(c.Name)
		cols[i] = grid.Column{
			Name: c.Name, NativeType: c.NativeType, Kind: c.Class.GridKind(),
			Width: columnWidth(c.Class), Sortable: c.Class.Sortable(), PrimaryKey: c.PrimaryKey,
			Editable: ok, Reason: why,
		}
	}
	return cols
}

// ---- requests ----

type navKind uint8

const (
	navFirst navKind = iota
	navNext
	navPrev
	navReload
)

func sortEq(a, b *database.Sort) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func filterEq(a, b *database.Filter) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// keyOf returns the text of the key columns of a row, in plan order.
func (d *TableData) keyOf(row []grid.Value) []string {
	key := make([]string, len(d.Plan.Key))
	for i, name := range d.Plan.Key {
		key[i] = row[d.Meta.ColumnIndex(name)].Text
	}
	return key
}

// requestFor builds the request of a navigation from the page in view. ok
// is false when the navigation does not apply (no next page, say).
func (d *TableData) requestFor(nav navKind) (req database.PageRequest, number int, ok bool) {
	req = database.PageRequest{Meta: d.Meta, Sort: d.Sort, Filter: d.Filter, Size: d.Size}
	cur := d.Page
	current := cur != nil && sortEq(cur.Req.Sort, d.Sort) && filterEq(cur.Req.Filter, d.Filter)
	keyset := d.Plan.Strategy == database.StrategyKeyset
	switch {
	case nav == navFirst, nav == navReload && !current:
		return req, 1, true
	case !current:
		return req, 0, false
	case nav == navReload:
		req.Offset, req.Cursor = cur.Req.Offset, cur.Req.Cursor
		return req, cur.Number, true
	case nav == navNext && cur.HasNext && len(cur.Rows) > 0:
		if keyset {
			req.Cursor = database.Cursor{Dir: database.CursorAfter, Key: d.keyOf(cur.Rows[len(cur.Rows)-1])}
		} else {
			req.Offset = cur.Req.Offset + d.Size
		}
		return req, cur.Number + 1, true
	case nav == navPrev && cur.HasPrev && len(cur.Rows) > 0:
		if keyset {
			req.Cursor = database.Cursor{Dir: database.CursorBefore, Key: d.keyOf(cur.Rows[0])}
		} else {
			req.Offset = max(0, cur.Req.Offset-d.Size)
		}
		return req, cur.Number - 1, true
	}
	return req, 0, false
}

// cacheKey identifies a page of the current walk: the query (sort and
// filter) and the position in it, so a page reached by turning back finds
// the page read when it was passed forward.
func (d *TableData) cacheKey(req database.PageRequest, number int) string {
	var b strings.Builder
	if req.Sort != nil {
		fmt.Fprintf(&b, "s%q%t;", req.Sort.Column, req.Sort.Desc)
	}
	if f := req.Filter; f != nil {
		fmt.Fprintf(&b, "f%q%d%q;", f.Column, f.Op, f.Value)
	}
	b.WriteString("#" + strconv.Itoa(number))
	return b.String()
}

// buildPage turns a result into the page in view.
func (d *TableData) buildPage(req database.PageRequest, number int, res *database.PageResult) *Page {
	p := &Page{Rows: res.Rows, Number: number, Req: req}
	if d.Plan.Strategy == database.StrategyKeyset {
		switch req.Cursor.Dir {
		case database.CursorFirst:
			p.HasNext = res.More
		case database.CursorAfter:
			p.HasPrev, p.HasNext = true, res.More
		case database.CursorBefore:
			p.HasPrev, p.HasNext = res.More, true
		case database.CursorLast:
			p.HasPrev = res.More
		}
	} else {
		p.HasPrev, p.HasNext = req.Offset > 0, res.More
	}
	if d.Meta.Identity.Known() {
		idx := make([]int, len(d.Meta.Identity.Columns))
		for i, name := range d.Meta.Identity.Columns {
			idx[i] = d.Meta.ColumnIndex(name)
		}
		p.Keys = make([]string, len(p.Rows))
		var b strings.Builder
		for r, row := range p.Rows {
			b.Reset()
			for _, i := range idx {
				b.WriteString(strconv.Itoa(int(row[i].Kind)) + ":" + row[i].Text + "\x1f")
			}
			p.Keys[r] = b.String()
		}
		p.IDs = make([]int64, len(p.Keys))
		for i, k := range p.Keys {
			p.IDs[i] = d.intern(k)
		}
	}
	return p
}

// ---- operations on the State ----

func tableTitle(t database.Table) string {
	return t.Ref.Name
}

// OpenTable opens the Table Inspector of a table of a connection, or
// focuses the tab already open on the same connection and table. A new tab
// reads its metadata, then its first page.
func (s *State) OpenTable(connectionID string, t database.Table) *Tab {
	key := TableKey{ConnectionID: connectionID, Ref: t.Ref}
	data := newTableData(key, t)
	tab, created := s.Workspace.OpenTable(key, tableTitle(t), data)
	if created {
		tab.Data = data
		s.loadMetadata(tab)
	}
	return tab
}

// loadMetadata reads the metadata of a table tab. The first read is
// followed by the first page; a refresh keeps the page in view when the
// layout did not change.
func (s *State) loadMetadata(tab *Tab) {
	d := tab.Data
	if d == nil {
		return
	}
	refresh := d.Meta != nil
	d.MetaErr = nil
	if !refresh {
		d.MetaState = OpLoading
	}
	ad, ok := s.Conns.SQL(d.Key.ConnectionID)
	o := d.metaOp.begin(s, d.Key.ConnectionID, MetadataTimeout)
	if !ok || o == nil {
		s.metadataFailed(d, noSession())
		return
	}
	id, ref := tab.ID, d.Key.Ref
	go func() {
		m, err := ad.DescribeTable(o.ctx, ref)
		s.post(func() { s.finishMetadata(id, d, o, m, err) })
	}()
}

func (s *State) metadataFailed(d *TableData, err *database.Error) {
	d.MetaErr = err
	if d.Meta == nil {
		d.MetaState = OpFailed
	}
}

func (s *State) liveTab(id TabID, d *TableData) *Tab {
	tab := s.Workspace.Find(id)
	if tab == nil || tab.Data != d {
		return nil
	}
	return tab
}

func (s *State) finishMetadata(id TabID, d *TableData, o *op, m *database.TableMetadata, err error) {
	ok := d.metaOp.finish(s, d.Key.ConnectionID, o)
	tab := s.liveTab(id, d)
	if !ok || tab == nil {
		return
	}
	if err != nil {
		s.metadataFailed(d, asDBError(err))
		return
	}
	first := d.Meta == nil
	old := d.Meta

	// A sort or filter on a column that is gone, or no longer applies, is
	// dropped rather than sent.
	sort, filter := d.Sort, d.Filter
	dropped := false
	if sort != nil {
		if c, ok := m.Column(sort.Column); !ok || !c.Class.Sortable() {
			sort, dropped = nil, true
		}
	}
	if filter != nil {
		if c, ok := m.Column(filter.Column); !ok || !database.Allows(c.Class, filter.Op) {
			filter, dropped = nil, true
		}
	}
	plan := database.PlanQuery(m, sort)
	reset := first || dropped || !sameLayout(old, m) || !slices.Equal(d.Plan.Key, plan.Key) || d.Plan.Strategy != plan.Strategy
	if (tab.Dirty() || d.SaveState == OpLoading) && (reset || !sameEditing(old, m)) {
		// The edits belong to the table as it was read. A refresh that would
		// change what they mean is not applied; refreshing never overwrites
		// edits.
		d.MetaErr = &database.Error{
			Kind:    database.KindMetadataUnavailable,
			Message: "The table structure changed since it was read.",
			Hint:    "Save or discard your edits, then refresh again.",
		}
		return
	}
	d.Meta, d.MetaState, d.MetaErr = m, OpReady, nil
	d.Sort, d.Filter, d.Plan = sort, filter, plan
	d.cols = buildColumns(m)
	if reset {
		d.cache.Clear()
		d.Page = nil
		d.PageSeq++
		s.fetchPage(tab, navFirst)
	}
}

// sameEditing reports whether the same columns can be edited in both reads of
// the metadata, and the rows are identified by the same key.
func sameEditing(a, b *database.TableMetadata) bool {
	if a == nil || b == nil || !slices.Equal(a.Identity.Columns, b.Identity.Columns) {
		return false
	}
	for _, c := range a.Columns {
		x, _ := a.CanEditColumn(c.Name)
		y, _ := b.CanEditColumn(c.Name)
		if x != y {
			return false
		}
	}
	return true
}

func sameLayout(a, b *database.TableMetadata) bool {
	if a == nil || b == nil || len(a.Columns) != len(b.Columns) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i].Name != b.Columns[i].Name || a.Columns[i].Class != b.Columns[i].Class {
			return false
		}
	}
	return true
}

// fetchPage reads a page of a table tab, or shows it from the cache.
func (s *State) fetchPage(tab *Tab, nav navKind) {
	d := tab.Data
	if d == nil || d.Meta == nil {
		return
	}
	req, number, ok := d.requestFor(nav)
	if !ok {
		return
	}
	if nav != navReload {
		if p, hit := d.cache.Get(d.cacheKey(req, number)); hit {
			d.pageOp.stop()
			d.Page, d.PageState, d.PageErr = p, OpReady, nil
			d.PageSeq++
			return
		}
	}
	d.PageState, d.PageErr = OpLoading, nil
	ad, ok := s.Conns.SQL(d.Key.ConnectionID)
	o := d.pageOp.begin(s, d.Key.ConnectionID, PageTimeout)
	if !ok || o == nil {
		d.PageState, d.PageErr = OpFailed, noSession()
		return
	}
	id := tab.ID
	go func() {
		res, err := ad.FetchPage(o.ctx, req)
		s.post(func() { s.finishPage(id, d, o, req, number, res, err) })
	}()
}

func (s *State) finishPage(id TabID, d *TableData, o *op, req database.PageRequest, number int, res *database.PageResult, err error) {
	ok := d.pageOp.finish(s, d.Key.ConnectionID, o)
	tab := s.liveTab(id, d)
	if !ok || tab == nil {
		return
	}
	if tab.Dirty() {
		// The edits belong to the rows in view: a read that was already on its
		// way does not replace them. (Save, Discard and Cancel decide first.)
		d.PageState, d.PageErr = OpReady, nil
		return
	}
	if err != nil {
		// The page in view stays, and the error is shown with it.
		d.PageState, d.PageErr = OpFailed, asDBError(err)
		return
	}
	p := d.buildPage(req, number, res)
	if !p.HasPrev && p.Number != 1 {
		// Rows were removed ahead of the walk: nothing is before this page,
		// so it is the first. The pages cached were numbered by the old walk.
		p.Number, number = 1, 1
		d.cache.Clear()
	}
	d.cache.Put(d.cacheKey(req, number), p)
	d.Page, d.PageState, d.PageErr = p, OpReady, nil
	d.Stale = false
	d.PageSeq++
}

// ReloadTable reads the page in view again, bypassing the cache. With unsaved
// edits it asks first (see PendingEdit).
func (s *State) ReloadTable(tab *Tab) {
	if tab.Data == nil {
		return
	}
	_ = s.guarded(tab, "reload the table", func() {
		d := tab.Data
		d.cache.Clear()
		if d.Meta == nil {
			s.loadMetadata(tab)
			return
		}
		s.fetchPage(tab, navReload)
	})
}

// RefreshMetadata reads the metadata of a table tab again. The page in view
// stays unless the layout of the table changed.
func (s *State) RefreshMetadata(tab *Tab) { s.loadMetadata(tab) }

// NextPage shows the page after the one in view. With unsaved edits it asks
// first.
func (s *State) NextPage(tab *Tab) {
	_ = s.guarded(tab, "go to the next page", func() { s.fetchPage(tab, navNext) })
}

// PrevPage shows the page before the one in view. With unsaved edits it asks
// first.
func (s *State) PrevPage(tab *Tab) {
	_ = s.guarded(tab, "go to the previous page", func() { s.fetchPage(tab, navPrev) })
}

// FirstPage shows the first page. With unsaved edits it asks first.
func (s *State) FirstPage(tab *Tab) {
	_ = s.guarded(tab, "go to the first page", func() { s.fetchPage(tab, navFirst) })
}

// SetSort sorts the table by a column, or clears the sort for nil. The first
// page of the new order is read; the request in flight stops. A sort the
// column cannot take is refused and changes nothing. With unsaved edits it
// asks first and returns ErrEditsPending: nothing changed yet.
func (s *State) SetSort(tab *Tab, sort *database.Sort) error {
	d := tab.Data
	if d == nil || d.Meta == nil {
		return nil
	}
	req := database.PageRequest{Meta: d.Meta, Sort: sort, Filter: d.Filter, Size: d.Size}
	if err := req.Validate(); err != nil {
		return err
	}
	if sortEq(sort, d.Sort) {
		return nil
	}
	return s.guarded(tab, "change the sort", func() {
		d.Sort = sort
		d.Plan = database.PlanQuery(d.Meta, sort)
		d.cache.Clear()
		s.fetchPage(tab, navFirst)
	})
}

// SetFilter sets the one column filter, or clears it for nil. The filter
// runs on the server: the first page of the result is read and the request
// in flight stops. A filter the column cannot take is refused. With unsaved
// edits it asks first and returns ErrEditsPending: nothing changed yet.
func (s *State) SetFilter(tab *Tab, f *database.Filter) error {
	d := tab.Data
	if d == nil || d.Meta == nil {
		return nil
	}
	req := database.PageRequest{Meta: d.Meta, Sort: d.Sort, Filter: f, Size: d.Size}
	if err := req.Validate(); err != nil {
		return err
	}
	if filterEq(f, d.Filter) {
		return nil
	}
	return s.guarded(tab, "change the filter", func() {
		d.Filter = f
		d.cache.Clear()
		s.fetchPage(tab, navFirst)
	})
}
