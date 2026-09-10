//go:build integration

package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type uiUser struct {
	ID   uuid.UUID
	Name string
	Tags []*uiTag
}

type uiTag struct {
	ID    uuid.UUID
	Label string
}

func uiUserMeta() drel.ModelMeta[uiUser] {
	return drel.ModelMeta[uiUser]{
		Table:     "ui_users",
		Columns:   []string{"id", "name"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*uiUser, error) {
			u := &uiUser{}
			return u, r.Scan(&u.ID, &u.Name)
		},
		PKValue:      func(u *uiUser) any { return u.ID },
		NormalizeKey: drel.NormalizeUUIDKey,
		ColumnValue:  func(u *uiUser, i int) any { return [...]any{u.ID, u.Name}[i] },
	}
}

func uiTagMeta() drel.ModelMeta[uiTag] {
	return drel.ModelMeta[uiTag]{
		Table:     "ui_tags",
		Columns:   []string{"id", "label"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*uiTag, error) {
			tg := &uiTag{}
			return tg, r.Scan(&tg.ID, &tg.Label)
		},
		PKValue:      func(tg *uiTag) any { return tg.ID },
		NormalizeKey: drel.NormalizeUUIDKey,
		ColumnValue:  func(tg *uiTag, i int) any { return [...]any{tg.ID, tg.Label}[i] },
	}
}

func TestIntegration_Include_ManyToMany_UUIDKeys(t *testing.T) {
	engine := setupTestDB(t)
	ctx := context.Background()

	for _, ddl := range []string{
		`CREATE TABLE ui_users (id UUID PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE ui_tags (id UUID PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE ui_user_tags (user_id UUID NOT NULL REFERENCES ui_users(id), tag_id UUID NOT NULL REFERENCES ui_tags(id), PRIMARY KEY (user_id, tag_id))`,
	} {
		_, err := engine.Exec(ctx, ddl)
		require.NoError(t, err)
	}

	u1 := uuid.New()
	tg1 := uuid.New()
	tg2 := uuid.New()
	_, err := engine.Exec(ctx, `INSERT INTO ui_users (id, name) VALUES ($1,$2)`, u1, "Alice")
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO ui_tags (id, label) VALUES ($1,$2),($3,$4)`, tg1, "fiction", tg2, "tech")
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO ui_user_tags (user_id, tag_id) VALUES ($1,$2),($3,$4)`, u1, tg1, u1, tg2)
	require.NoError(t, err)

	userMeta := uiUserMeta()
	tagMeta := uiTagMeta()
	tagsRel := &drel.RelationInfo{
		Name:        "Tags",
		Type:        drel.ManyToMany,
		FKColumn:    "user_id",
		JoinTable:   "ui_user_tags",
		RefColumn:   "tag_id",
		RelatedMeta: drel.ToMetaBase(&tagMeta),
		FieldSetter: func(parent any, related any) {
			u := parent.(*uiUser)
			for _, it := range related.([]any) {
				u.Tags = append(u.Tags, it.(*uiTag))
			}
		},
	}

	repo := drel.NewRepository(engine, userMeta)
	user, err := repo.Include(drel.NewIncludeSpec(tagsRel)).Find(ctx, u1)
	require.NoError(t, err)
	require.Len(t, user.Tags, 2, "pgx scans UUID pivot cols as [16]byte; NormalizeKey must map them to uuid.UUID")

	labels := []string{user.Tags[0].Label, user.Tags[1].Label}
	assert.ElementsMatch(t, []string{"fiction", "tech"}, labels)
}
