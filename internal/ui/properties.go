package ui

import (
	"fmt"
	"strings"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/database"
)

// properties draws the Properties section of the Table Inspector (spec
// FR-003): columns, constraints, foreign keys in both directions, indexes
// and triggers. A section that was not read says why, rather than looking
// empty.
func properties(c *mygoui.Context, t *mygoui.Theme, m *database.TableMetadata) {
	mygoui.Scroll(c).Grow(1).Children(func() {
		mygoui.Column(c).Padding(12).Gap(14).Children(func() {
			propSummary(c, t, m)
			propColumns(c, t, m)
			propSection(c, t, "Constraints", m.Constraints.State, m.Constraints.Reason, len(m.Constraints.Items), func() {
				rows := make([][]string, len(m.Constraints.Items))
				for i, k := range m.Constraints.Items {
					rows[i] = []string{k.Type.String(), k.Name, strings.Join(k.Columns, ", "), k.Definition}
				}
				propTable(c, t, []float32{110, 200, 200, 420}, []string{"Type", "Name", "Columns", "Definition"}, rows)
			})
			propSection(c, t, "Primary key", m.Constraints.State, m.Constraints.Reason, len(m.PrimaryKey), func() {
				mygoui.Text(c, strings.Join(m.PrimaryKey, ", "))
			})
			propSection(c, t, "Foreign keys (references)", m.ForeignKeys.State, m.ForeignKeys.Reason, len(m.ForeignKeys.Items), func() {
				rows := make([][]string, len(m.ForeignKeys.Items))
				for i, k := range m.ForeignKeys.Items {
					rows[i] = []string{k.Name, strings.Join(k.Columns, ", "), refName(k.RefTable), strings.Join(k.RefColumns, ", "), k.OnUpdate, k.OnDelete}
				}
				propTable(c, t, []float32{200, 180, 200, 180, 110, 110},
					[]string{"Name", "Columns", "Referenced table", "Referenced columns", "On update", "On delete"}, rows)
			})
			propSection(c, t, "Referenced by (incoming foreign keys)", m.Referenced.State, m.Referenced.Reason, len(m.Referenced.Items), func() {
				rows := make([][]string, len(m.Referenced.Items))
				for i, k := range m.Referenced.Items {
					rows[i] = []string{k.Name, refName(k.Table), strings.Join(k.Columns, ", "), strings.Join(k.RefColumns, ", "), k.OnDelete}
				}
				propTable(c, t, []float32{200, 200, 180, 180, 110},
					[]string{"Name", "Table", "Columns", "Points to", "On delete"}, rows)
			})
			propSection(c, t, "Indexes", m.Indexes.State, m.Indexes.Reason, len(m.Indexes.Items), func() {
				rows := make([][]string, len(m.Indexes.Items))
				for i, k := range m.Indexes.Items {
					rows[i] = []string{k.Name, strings.Join(k.Columns, ", "), k.Method, indexFlags(k), k.Predicate}
				}
				propTable(c, t, []float32{200, 260, 90, 200, 260}, []string{"Name", "Columns", "Type", "Properties", "Condition"}, rows)
			})
			propSection(c, t, "Triggers", m.Triggers.State, m.Triggers.Reason, len(m.Triggers.Items), func() {
				rows := make([][]string, len(m.Triggers.Items))
				for i, k := range m.Triggers.Items {
					rows[i] = []string{k.Name, k.Timing, strings.Join(k.Events, ", "), k.Enabled}
				}
				propTable(c, t, []float32{220, 110, 220, 120}, []string{"Name", "Timing", "Event", "Status"}, rows)
			})
		})
	})
}

func refName(r database.TableRef) string {
	if r.Schema == "" {
		return r.Name
	}
	return r.Schema + "." + r.Name
}

func indexFlags(ix database.IndexMetadata) string {
	var f []string
	if ix.Primary {
		f = append(f, "primary")
	}
	if ix.Unique {
		f = append(f, "unique")
	}
	if ix.Partial {
		f = append(f, "partial")
	}
	if !ix.Valid {
		f = append(f, "invalid")
	}
	return strings.Join(f, ", ")
}

func propSummary(c *mygoui.Context, t *mygoui.Theme, m *database.TableMetadata) {
	kind := m.Kind.String()
	if m.Subtype != "" {
		kind = m.Subtype
	}
	mygoui.Column(c).Gap(4).Children(func() {
		mygoui.Text(c, refName(m.Ref)).Bold().FontSize(15)
		line := kind
		switch {
		case m.EstimatedRows >= 0:
			line += fmt.Sprintf(" · about %s rows (the server's estimate)", formatCount(int(m.EstimatedRows)))
		case m.Kind == database.ObjectTable:
			line += " · row count unknown (the table was not analyzed)"
		}
		mygoui.Text(c, line).TextColor(t.TextMuted)
		if m.Identity.Known() {
			mygoui.Text(c, fmt.Sprintf("Row identity: %s (%s)", strings.Join(m.Identity.Columns, ", "), m.Identity)).TextColor(t.TextMuted)
		} else {
			mygoui.Text(c, "No verified row identity. "+m.Identity.Reason).TextColor(t.Warning)
		}
	})
}

func propColumns(c *mygoui.Context, t *mygoui.Theme, m *database.TableMetadata) {
	mygoui.Column(c).Gap(4).Children(func() {
		mygoui.Text(c, fmt.Sprintf("Columns (%d)", len(m.Columns))).Bold()
		rows := make([][]string, len(m.Columns))
		for i, col := range m.Columns {
			def := ""
			if col.Default != nil {
				def = *col.Default
			}
			ident := col.Identity.String()
			if col.Generated {
				ident = "generated"
			}
			rows[i] = []string{
				fmt.Sprint(col.Ordinal), col.Name, col.Class.String(), col.NativeType,
				yesNo(col.Nullable), def, yesNo(col.PrimaryKey), yesNo(col.Unique), ident,
			}
		}
		propTable(c, t, []float32{40, 200, 110, 200, 70, 240, 50, 70, 100},
			[]string{"#", "Name", "Type", "Native type", "Nullable", "Default", "PK", "Unique", "Identity"}, rows)
	})
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// propSection draws a titled section: its table when it was read and has
// items, "None" when it was read and is empty, and the reason when it was
// not read.
func propSection(c *mygoui.Context, t *mygoui.Theme, title string, state database.SectionState, reason string, n int, body func()) {
	mygoui.Column(c).Gap(4).Children(func() {
		mygoui.Text(c, title).Bold()
		switch {
		case state == database.SectionNotApplicable:
			mygoui.Text(c, "Not applicable. "+reason).TextColor(t.TextMuted)
		case state != database.SectionAvailable:
			mygoui.Text(c, "Not available. "+reason).TextColor(t.Warning)
		case n == 0:
			mygoui.Text(c, "None").TextColor(t.TextMuted)
		default:
			body()
		}
	})
}

// propTable draws rows of text under a header, in columns of fixed width.
func propTable(c *mygoui.Context, t *mygoui.Theme, widths []float32, header []string, rows [][]string) {
	line := func(cells []string, bold bool) {
		mygoui.Row(c).Gap(8).Children(func() {
			for i, s := range cells {
				txt := mygoui.Text(c, s).SingleLine().Width(widths[i]).Tooltip(s)
				if bold {
					txt.Bold().TextColor(t.TextMuted)
				}
			}
		})
	}
	mygoui.Column(c).Gap(2).Children(func() {
		line(header, true)
		mygoui.Divider(c)
		for _, r := range rows {
			line(r, false)
		}
	})
}
