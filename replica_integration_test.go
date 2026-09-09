//go:build integration

package drel_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupTestDSN starts a Postgres container and returns its connection string.
// It mirrors setupTestDB (integration_test.go) but exposes the DSN so a second
// engine can register the same instance as a read replica.
func setupTestDSN(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("dreltest"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	return connStr
}

// replicaRow is a minimal model for the failover test.
type replicaRow struct {
	ID int
	V  string
}

var replicaRowMeta = drel.ModelMeta[replicaRow]{
	Table:     "rep_t",
	Columns:   []string{"id", "v"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*replicaRow, error) {
		r := &replicaRow{}
		return r, row.Scan(&r.ID, &r.V)
	},
	PKValue:       func(r *replicaRow) any { return r.ID },
	InsertColumns: func(r *replicaRow) ([]string, []any) { return []string{"id", "v"}, []any{r.ID, r.V} },
}

// TestIntegration_ReplicaFailover registers an unreachable replica plus the live
// primary as a replica; round-robin reads must fail over and succeed rather than
// surfacing the dead replica's connection error.
func TestIntegration_ReplicaFailover(t *testing.T) {
	primaryDSN := setupTestDSN(t)
	// A replica DSN pointing at a closed port: pgx connect fails on the read.
	deadDSN := "postgres://test:test@127.0.0.1:1/dreltest?sslmode=disable&connect_timeout=1"

	e, err := drel.NewEngine(primaryDSN,
		drel.WithReadReplica(deadDSN),
		drel.WithReadReplica(primaryDSN), // a live replica so failover has somewhere to land
	)
	require.NoError(t, err)
	t.Cleanup(e.Close)

	ctx := context.Background()
	_, err = e.Exec(ctx, `CREATE TABLE rep_t (id int primary key, v text)`)
	require.NoError(t, err)
	_, err = e.Exec(ctx, `INSERT INTO rep_t (id, v) VALUES (1, 'a')`)
	require.NoError(t, err)

	repo := drel.NewRepository(e, replicaRowMeta)
	// Issue several reads so round-robin lands on the dead replica at least once;
	// every read must still succeed via failover to a live target.
	for i := 0; i < 6; i++ {
		rows, err := repo.All(ctx)
		require.NoError(t, err, "read %d should fail over to a live target", i)
		require.Len(t, rows, 1)
		require.Equal(t, "a", rows[0].V)
	}
}

// repParent/repChild model a HasMany relation for the include-primary test.
type repParent struct {
	ID       int
	Name     string
	Children []*repChild
}

type repChild struct {
	ID  int
	PID int
}

var repParentMeta = drel.ModelMeta[repParent]{
	Table:     "rip_parent",
	Columns:   []string{"id", "name"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*repParent, error) {
		p := &repParent{}
		return p, row.Scan(&p.ID, &p.Name)
	},
	PKValue:       func(p *repParent) any { return p.ID },
	InsertColumns: func(p *repParent) ([]string, []any) { return []string{"id", "name"}, []any{p.ID, p.Name} },
}

var repChildMeta = drel.ModelMeta[repChild]{
	Table:     "rip_child",
	Columns:   []string{"id", "pid"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*repChild, error) {
		c := &repChild{}
		return c, row.Scan(&c.ID, &c.PID)
	},
	PKValue: func(c *repChild) any { return c.ID },
	ColumnValue: func(c *repChild, i int) any {
		if i == 1 {
			return c.PID
		}
		return c.ID
	},
}

var repChildrenRelation = &drel.RelationInfo{
	Name:        "Children",
	Type:        drel.HasMany,
	FKColumn:    "pid",
	RelatedMeta: drel.ToMetaBase(&repChildMeta),
	FieldSetter: func(parent any, related any) {
		p := parent.(*repParent)
		for _, r := range related.([]any) {
			p.Children = append(p.Children, r.(*repChild))
		}
	},
}

func TestIntegration_IncludePrimary_ReadYourWrites(t *testing.T) {
	primaryDSN := setupTestDSN(t)
	e, err := drel.NewEngine(primaryDSN, drel.WithReadReplica(primaryDSN))
	require.NoError(t, err)
	t.Cleanup(e.Close)

	ctx := context.Background()
	_, err = e.Exec(ctx, `CREATE TABLE rip_parent (id int primary key, name text)`)
	require.NoError(t, err)
	_, err = e.Exec(ctx, `CREATE TABLE rip_child (id int primary key, pid int)`)
	require.NoError(t, err)
	_, err = e.Exec(ctx, `INSERT INTO rip_parent (id, name) VALUES (1, 'p1')`)
	require.NoError(t, err)
	_, err = e.Exec(ctx, `INSERT INTO rip_child (id, pid) VALUES (10, 1), (11, 1)`)
	require.NoError(t, err)

	repo := drel.NewRepository(e, repParentMeta)
	parent, err := repo.Include(drel.NewIncludeSpec(repChildrenRelation)).Primary().Find(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "p1", parent.Name)
	require.Len(t, parent.Children, 2, "primary-routed include must load both children")
}

func TestIntegration_SelectPrimary_ReadYourWrites(t *testing.T) {
	primaryDSN := setupTestDSN(t)
	e, err := drel.NewEngine(primaryDSN, drel.WithReadReplica(primaryDSN))
	require.NoError(t, err)
	t.Cleanup(e.Close)

	ctx := context.Background()
	_, err = e.Exec(ctx, `CREATE TABLE rep_t (id int primary key, v text)`)
	require.NoError(t, err)
	_, err = e.Exec(ctx, `INSERT INTO rep_t (id, v) VALUES (1, 'x'), (2, 'y')`)
	require.NoError(t, err)

	repo := drel.NewRepository(e, replicaRowMeta)
	type vDTO struct {
		V string `db:"v"`
	}
	rows, err := drel.Select[vDTO](ctx, repo.Primary().OrderBy(drel.NewOrderedCol[int]("id").Asc()), drel.ColRef("v"))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "x", rows[0].V)
	require.Equal(t, "y", rows[1].V)
}
