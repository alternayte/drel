package testmodels

import (
	"github.com/alternayte/drel"
)

// JSONDoc exercises JSON (jsonb) and JSON-array columns end-to-end.
type JSONDoc struct {
	drel.Model[int]
	Tags []string          `db:"tags"`
	Meta map[string]string `db:"meta"`
}

var JSONDocMeta = drel.ModelMeta[JSONDoc]{
	Table:     "json_docs",
	Columns:   []string{"id", "tags", "meta", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*JSONDoc, error) {
		p := &JSONDoc{}
		idPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()
		err := row.Scan(idPtr, drel.JSON[[]string]{V: &p.Tags}, drel.JSON[map[string]string]{V: &p.Meta}, createdAtPtr, updatedAtPtr)
		if err != nil {
			return nil, err
		}
		return p, nil
	},
	Snapshot: func(p *JSONDoc) any { return jsonDocSnapshot{Tags: p.Tags, Meta: p.Meta} },
	Diff: func(p *JSONDoc, snap any) []drel.FieldChange {
		s := snap.(jsonDocSnapshot)
		var changes []drel.FieldChange
		if !slicesEqual(p.Tags, s.Tags) {
			changes = append(changes, drel.FieldChange{Column: "tags", Value: drel.JSON[[]string]{V: &p.Tags}})
		}
		if !mapsEqual(p.Meta, s.Meta) {
			changes = append(changes, drel.FieldChange{Column: "meta", Value: drel.JSON[map[string]string]{V: &p.Meta}})
		}
		return changes
	},
	PKValue: func(p *JSONDoc) any { return p.ID() },
	InsertColumns: func(p *JSONDoc) ([]string, []any) {
		return []string{"tags", "meta"}, []any{drel.JSON[[]string]{V: &p.Tags}, drel.JSON[map[string]string]{V: &p.Meta}}
	},
	ScanReturning: func(p *JSONDoc, row drel.Row) error {
		idPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()
		return row.Scan(idPtr, createdAtPtr, updatedAtPtr)
	},
	ColumnValue: func(p *JSONDoc, idx int) any {
		switch idx {
		case 0:
			return p.ID()
		case 1:
			return drel.JSON[[]string]{V: &p.Tags}
		case 2:
			return drel.JSON[map[string]string]{V: &p.Meta}
		case 3:
			return p.CreatedAt()
		case 4:
			return p.UpdatedAt()
		}
		return nil
	},
	NormalizeKey: func(v any) any { return drel.NormalizeIntKey(v) },
}

type jsonDocSnapshot struct {
	Tags []string
	Meta map[string]string
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
