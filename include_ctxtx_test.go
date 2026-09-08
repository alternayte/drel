package drel_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- parent (team) / child (member) models with full tracking metadata ---

type uowTeam struct {
	ID        int
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	Members   []*uowMember
}

type uowMember struct {
	ID        int
	TeamID    int
	Nick      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

var uowTeamMeta = drel.ModelMeta[uowTeam]{
	Table:    "uow_teams",
	Columns:  []string{"id", "name", "created_at", "updated_at"},
	PKColumn: "id",
	Scan: func(row drel.Row) (*uowTeam, error) {
		x := &uowTeam{}
		err := row.Scan(&x.ID, &x.Name, &x.CreatedAt, &x.UpdatedAt)
		return x, err
	},
	Snapshot: func(x *uowTeam) any { return x.Name },
	Diff: func(x *uowTeam, snap any) []drel.FieldChange {
		if x.Name != snap.(string) {
			return []drel.FieldChange{{Column: "name", Value: x.Name}}
		}
		return nil
	},
	PKValue:       func(x *uowTeam) any { return x.ID },
	ColumnValue:   func(x *uowTeam, i int) any { switch i { case 0: return x.ID; case 1: return x.Name; case 2: return x.CreatedAt; case 3: return x.UpdatedAt }; return nil },
	InsertColumns: func(x *uowTeam) ([]string, []any) { return []string{"name"}, []any{x.Name} },
	ScanReturning: func(x *uowTeam, row drel.Row) error { return row.Scan(&x.ID, &x.CreatedAt, &x.UpdatedAt) },
}

var uowMemberMeta = drel.ModelMeta[uowMember]{
	Table:    "uow_members",
	Columns:  []string{"id", "team_id", "nick", "created_at", "updated_at"},
	PKColumn: "id",
	Scan: func(row drel.Row) (*uowMember, error) {
		x := &uowMember{}
		err := row.Scan(&x.ID, &x.TeamID, &x.Nick, &x.CreatedAt, &x.UpdatedAt)
		return x, err
	},
	Snapshot: func(x *uowMember) any { return x.Nick },
	Diff: func(x *uowMember, snap any) []drel.FieldChange {
		if x.Nick != snap.(string) {
			return []drel.FieldChange{{Column: "nick", Value: x.Nick}}
		}
		return nil
	},
	PKValue:       func(x *uowMember) any { return x.ID },
	ColumnValue:   func(x *uowMember, i int) any { switch i { case 0: return x.ID; case 1: return x.TeamID; case 2: return x.Nick; case 3: return x.CreatedAt; case 4: return x.UpdatedAt }; return nil },
	InsertColumns: func(x *uowMember) ([]string, []any) { return []string{"team_id", "nick"}, []any{x.TeamID, x.Nick} },
	ScanReturning: func(x *uowMember, row drel.Row) error { return row.Scan(&x.ID, &x.CreatedAt, &x.UpdatedAt) },
}

func membersRel() drel.IncludeSpec {
	return drel.NewIncludeSpec(&drel.RelationInfo{
		Name:        "Members",
		Type:        drel.HasMany,
		FKColumn:    "team_id",
		RelatedMeta: drel.ToMetaBase(&uowMemberMeta),
		FieldSetter: func(parent any, related any) {
			t := parent.(*uowTeam)
			for _, r := range related.([]any) {
				t.Members = append(t.Members, r.(*uowMember))
			}
		},
	})
}

func setupUoWIncludeEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE uow_teams (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `CREATE TABLE uow_members (
		id INTEGER PRIMARY KEY AUTOINCREMENT, team_id INTEGER NOT NULL, nick TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO uow_teams (name) VALUES ('Alpha')`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO uow_members (team_id, nick) VALUES (1, 'old-nick')`)
	require.NoError(t, err)
	return engine
}

func TestCtxTxInclude_EditedChildIsPersisted(t *testing.T) {
	engine := setupUoWIncludeEngine(t)
	ctx := context.Background()

	var childID int
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		teams := drel.NewTxRepository(drel.MustFromContext(ctx), uowTeamMeta)

		team, err := teams.Include(membersRel()).Find(ctx, 1)
		require.NoError(t, err)
		require.Len(t, team.Members, 1)

		// Edit a field on the INCLUDE'd child.
		team.Members[0].Nick = "new-nick"
		childID = team.Members[0].ID
		return nil
	}))

	// Re-read on a fresh repo to confirm the edit was flushed.
	reread, err := drel.NewRepository(engine, uowMemberMeta).Find(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, "new-nick", reread.Nick, "an edited Include'd child must be persisted by the commit")
}
