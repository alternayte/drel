package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type umUser struct {
	ID   uuid.UUID
	Name string
	Tags []*umTag
}

type umTag struct {
	ID    uuid.UUID
	Label string
}

func umUserMeta() drel.ModelMeta[umUser] {
	return drel.ModelMeta[umUser]{
		Table:     "um_users",
		Columns:   []string{"id", "name"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*umUser, error) {
			u := &umUser{}
			var id string
			if err := r.Scan(&id, &u.Name); err != nil {
				return nil, err
			}
			parsed, err := uuid.Parse(id)
			if err != nil {
				return nil, err
			}
			u.ID = parsed
			return u, nil
		},
		PKValue:      func(u *umUser) any { return u.ID },
		NormalizeKey: drel.NormalizeUUIDKey,
		ColumnValue: func(u *umUser, i int) any {
			switch i {
			case 0:
				return u.ID.String()
			case 1:
				return u.Name
			}
			return nil
		},
	}
}

func umTagMeta() drel.ModelMeta[umTag] {
	return drel.ModelMeta[umTag]{
		Table:     "um_tags",
		Columns:   []string{"id", "label"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*umTag, error) {
			tg := &umTag{}
			var id string
			if err := r.Scan(&id, &tg.Label); err != nil {
				return nil, err
			}
			parsed, err := uuid.Parse(id)
			if err != nil {
				return nil, err
			}
			tg.ID = parsed
			return tg, nil
		},
		PKValue:      func(tg *umTag) any { return tg.ID },
		NormalizeKey: drel.NormalizeUUIDKey,
		ColumnValue: func(tg *umTag, i int) any {
			switch i {
			case 0:
				return tg.ID.String()
			case 1:
				return tg.Label
			}
			return nil
		},
	}
}

func TestInclude_ManyToMany_UUIDKeys_SQLite(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()

	u1 := uuid.New()
	t1 := uuid.New()
	t2 := uuid.New()

	for _, ddl := range []string{
		`CREATE TABLE um_users (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE um_tags (id TEXT PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE um_user_tags (user_id TEXT NOT NULL, tag_id TEXT NOT NULL, PRIMARY KEY (user_id, tag_id))`,
	} {
		_, err := engine.Exec(ctx, ddl)
		require.NoError(t, err)
	}
	_, err = engine.Exec(ctx, `INSERT INTO um_users (id, name) VALUES (?, ?)`, u1.String(), "Alice")
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO um_tags (id, label) VALUES (?, ?), (?, ?)`, t1.String(), "fiction", t2.String(), "tech")
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO um_user_tags (user_id, tag_id) VALUES (?, ?), (?, ?)`, u1.String(), t1.String(), u1.String(), t2.String())
	require.NoError(t, err)

	userMeta := umUserMeta()
	tagMeta := umTagMeta()
	tagsRel := drel.RelationInfo{
		Name:        "Tags",
		Type:        drel.ManyToMany,
		FKColumn:    "user_id",
		JoinTable:   "um_user_tags",
		RefColumn:   "tag_id",
		RelatedMeta: drel.ToMetaBase(&tagMeta),
		FieldSetter: func(parent any, related any) {
			u := parent.(*umUser)
			for _, it := range related.([]any) {
				u.Tags = append(u.Tags, it.(*umTag))
			}
		},
	}

	repo := drel.NewRepository(engine, userMeta)
	user, err := repo.Include(drel.NewIncludeSpec(&tagsRel)).Find(ctx, u1)
	require.NoError(t, err)
	require.Len(t, user.Tags, 2, "UUID-keyed M2M must return the related collection, not empty")

	labels := []string{user.Tags[0].Label, user.Tags[1].Label}
	assert.ElementsMatch(t, []string{"fiction", "tech"}, labels)
}
