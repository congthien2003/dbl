package database

import "testing"

func keyed(cols ...ColumnMetadata) *TableMetadata {
	m := tableWith(cols...)
	m.Constraints = okSection(ConstraintMetadata{Type: ConstraintPrimaryKey, Columns: []string{cols[0].Name}})
	m.ResolveIdentity()
	return m
}

func TestPlanKeysetWithoutSort(t *testing.T) {
	m := keyed(col("id", ClassNumber, false), col("name", ClassText, true))
	p := PlanQuery(m, nil)
	if p.Strategy != StrategyKeyset || !p.Stable || len(p.Key) != 1 || p.Key[0] != "id" || len(p.Order) != 1 {
		t.Fatalf("plan %+v", p)
	}
}

func TestPlanOffsetWithSortAddsTieBreaker(t *testing.T) {
	m := keyed(col("id", ClassNumber, false), col("name", ClassText, true))
	p := PlanQuery(m, &Sort{Column: "name", Desc: true})
	if p.Strategy != StrategyOffset || !p.Stable {
		t.Fatalf("plan %+v", p)
	}
	if len(p.Order) != 2 || p.Order[0] != (OrderColumn{"name", true}) || p.Order[1] != (OrderColumn{"id", false}) {
		t.Errorf("order %+v", p.Order)
	}
	// Sorting by the key itself needs no second column.
	p = PlanQuery(m, &Sort{Column: "id"})
	if len(p.Order) != 1 || p.Strategy != StrategyOffset {
		t.Errorf("plan %+v", p)
	}
}

func TestPlanWithoutIdentityWarns(t *testing.T) {
	m := tableWith(col("a", ClassText, true))
	m.ResolveIdentity()
	for _, s := range []*Sort{nil, {Column: "a"}} {
		p := PlanQuery(m, s)
		if p.Stable || p.Warning == "" || p.Strategy != StrategyOffset {
			t.Errorf("sort %v: plan %+v", s, p)
		}
		if s == nil && len(p.Order) != 0 {
			t.Errorf("an ORDER BY was invented: %+v", p.Order)
		}
	}
}

func TestPlanKeyThatCannotWalkFallsBackToOffset(t *testing.T) {
	m := keyed(col("blob", ClassBinary, false), col("x", ClassText, true))
	p := PlanQuery(m, nil)
	if p.Strategy != StrategyOffset {
		t.Fatalf("plan %+v", p)
	}
	if p.Stable {
		t.Error("a key that cannot be ordered cannot make the order stable")
	}
}

func TestValidateRequest(t *testing.T) {
	m := keyed(col("id", ClassNumber, false), col("doc", ClassJSON, true), col("name", ClassText, true))
	base := PageRequest{Meta: m, Size: 100}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	bad := map[string]struct {
		req  PageRequest
		kind Kind
	}{
		"no meta":        {PageRequest{Size: 10}, KindValidation},
		"zero size":      {PageRequest{Meta: m}, KindValidation},
		"huge size":      {PageRequest{Meta: m, Size: MaxPageSize + 1}, KindValidation},
		"negative off":   {PageRequest{Meta: m, Size: 10, Offset: -1}, KindValidation},
		"sort ghost":     {PageRequest{Meta: m, Size: 10, Sort: &Sort{Column: "ghost"}}, KindValidation},
		"sort json":      {PageRequest{Meta: m, Size: 10, Sort: &Sort{Column: "doc"}}, KindUnsupported},
		"filter ghost":   {PageRequest{Meta: m, Size: 10, Filter: &Filter{Column: "ghost", Op: OpIsNull}}, KindValidation},
		"contains on id": {PageRequest{Meta: m, Size: 10, Filter: &Filter{Column: "id", Op: OpContains, Value: "1"}}, KindUnsupported},
		"equals on json": {PageRequest{Meta: m, Size: 10, Filter: &Filter{Column: "doc", Op: OpEquals, Value: "1"}}, KindUnsupported},
		"offset w/ key":  {PageRequest{Meta: m, Size: 10, Offset: 5}, KindValidation},
		"after no key":   {PageRequest{Meta: m, Size: 10, Cursor: Cursor{Dir: CursorAfter}}, KindValidation},
		"first w/ key":   {PageRequest{Meta: m, Size: 10, Cursor: Cursor{Dir: CursorFirst, Key: []string{"1"}}}, KindValidation},
		"cursor offset":  {PageRequest{Meta: m, Size: 10, Sort: &Sort{Column: "name"}, Cursor: Cursor{Dir: CursorLast}}, KindValidation},
	}
	for name, tc := range bad {
		t.Run(name, func(t *testing.T) {
			err := tc.req.Validate()
			if err == nil {
				t.Fatal("accepted")
			}
			if KindOf(err) != tc.kind {
				t.Errorf("kind %v, want %v (%v)", KindOf(err), tc.kind, err)
			}
		})
	}
	ok := PageRequest{Meta: m, Size: 10, Cursor: Cursor{Dir: CursorAfter, Key: []string{"7"}}}
	if err := ok.Validate(); err != nil {
		t.Errorf("keyset cursor: %v", err)
	}
	ok = PageRequest{Meta: m, Size: 10, Offset: 20, Sort: &Sort{Column: "name"}}
	if err := ok.Validate(); err != nil {
		t.Errorf("offset page: %v", err)
	}
}

func TestPlanDescribe(t *testing.T) {
	m := keyed(col("id", ClassNumber, false), col("name", ClassText, true))
	if got := PlanQuery(m, nil).Describe(); got != "Order: id (keyset paging)" {
		t.Errorf("describe %q", got)
	}
	if got := PlanQuery(m, &Sort{Column: "name", Desc: true}).Describe(); got != "Order: name ↓, id (offset paging)" {
		t.Errorf("describe %q", got)
	}
}
