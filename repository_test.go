package drel

import "testing"

type ksModel struct{ Model[string] }

func TestToMetaBase_CarriesKeyFields(t *testing.T) {
	meta := ModelMeta[ksModel]{
		Table:       "ks_models",
		PKColumns:   []string{"id"},
		KeyStrategy: KeyAppAssigned,
		GenerateKey: func() any { return "generated" },
		SetKey:      func(p *ksModel, key any) { p.SetID(key.(string)) },
		KeyIsZero:   func(p *ksModel) bool { return p.ID() == "" },
		PKValue:     func(p *ksModel) any { return p.ID() },
		InsertColumns: func(p *ksModel) ([]string, []any) {
			return []string{}, []any{}
		},
		Scan: func(Row) (*ksModel, error) { return &ksModel{}, nil },
	}

	base := ToMetaBase(&meta)
	if base.KeyStrategy != KeyAppAssigned {
		t.Fatal("KeyStrategy not carried")
	}
	if base.GenerateKey == nil || base.GenerateKey().(string) != "generated" {
		t.Fatal("GenerateKey not carried")
	}
	p := &ksModel{}
	if !base.KeyIsZero(p) {
		t.Fatal("KeyIsZero should be true for empty id")
	}
	base.SetKey(p, "x")
	if p.ID() != "x" {
		t.Fatal("SetKey did not set id")
	}
}
