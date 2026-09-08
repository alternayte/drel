package drel

import (
	"context"
	"errors"
	"testing"

	"github.com/alternayte/drel/internal/driver"
)

// recDriver is a no-op driver.Driver that records which queries/execs it served.
type recDriver struct {
	name  string
	calls *[]string
}

func (d *recDriver) record(kind string) { *d.calls = append(*d.calls, d.name+":"+kind) }

func (d *recDriver) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	d.record("queryrow")
	return nil
}
func (d *recDriver) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	d.record("query")
	return nil, nil
}
func (d *recDriver) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	d.record("exec")
	return 0, nil
}
func (d *recDriver) Begin(ctx context.Context) (driver.Tx, error) { d.record("begin"); return nil, nil }
func (d *recDriver) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	d.record("begintx")
	return nil, nil
}
func (d *recDriver) Close()                         {}
func (d *recDriver) Ping(ctx context.Context) error { d.record("ping"); return nil }
func (d *recDriver) Stat() driver.PoolStat          { d.record("stat"); return driver.PoolStat{} }

func TestReadDriver_RoundRobinAndPrimary(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	r1 := &recDriver{name: "r1", calls: &calls}
	r2 := &recDriver{name: "r2", calls: &calls}

	e := &Engine{drv: primary, replicas: []driver.Driver{r1, r2}}

	pick := func() string {
		d := e.readDriver(false).(*recDriver)
		return d.name
	}
	// Round-robin across replicas only.
	got := []string{pick(), pick(), pick(), pick()}
	want := []string{"r1", "r2", "r1", "r2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("read %d: got %s want %s (seq %v)", i, got[i], want[i], got)
		}
	}

	// primary=true forces the primary.
	if e.readDriver(true).(*recDriver).name != "primary" {
		t.Fatal("readDriver(true) should return the primary")
	}

	// With no replicas, reads use the primary.
	e2 := &Engine{drv: primary}
	if e2.readDriver(false).(*recDriver).name != "primary" {
		t.Fatal("readDriver(false) with no replicas should return the primary")
	}
}

func TestReadRouting_WritesAndTxUsePrimary(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	r1 := &recDriver{name: "r1", calls: &calls}
	e := &Engine{drv: primary, replicas: []driver.Driver{r1}}
	ctx := context.Background()

	_, _ = e.queryInternal(ctx, "SELECT 1")      // read → replica
	_, _ = e.execInternal(ctx, "UPDATE x SET y") // write → primary
	_, _ = e.drv.Begin(ctx)                      // tx begin is always on primary by construction

	// First call should be the replica; the write must be on the primary.
	if calls[0] != "r1:query" {
		t.Fatalf("read should hit replica, got %v", calls)
	}
	foundPrimaryExec := false
	for _, c := range calls {
		if c == "primary:exec" {
			foundPrimaryExec = true
		}
		if c == "r1:exec" {
			t.Fatalf("write must never hit a replica: %v", calls)
		}
	}
	if !foundPrimaryExec {
		t.Fatalf("write should hit primary: %v", calls)
	}
}

// errDriver is a recDriver that fails Query/QueryRow with a fixed error.
type errDriver struct {
	recDriver
	err error
}

func (d *errDriver) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	d.record("query")
	return nil, d.err
}

func (d *errDriver) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	d.record("queryrow")
	return nil // a failing replica returns a nil row; the failover loop must skip it
}

func TestQueryRouted_FailsOverToNextReplicaThenPrimary(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	bad := &errDriver{recDriver: recDriver{name: "bad", calls: &calls}, err: errors.New("boom")}
	e := &Engine{drv: primary, replicas: []driver.Driver{bad}}
	ctx := context.Background()

	rows, err := e.queryRouted(ctx, false, "SELECT 1")
	if err != nil {
		t.Fatalf("expected failover to succeed, got error: %v", err)
	}
	_ = rows
	// The bad replica is tried first, then the primary serves the read.
	if len(calls) < 2 || calls[0] != "bad:query" || calls[len(calls)-1] != "primary:query" {
		t.Fatalf("expected bad replica then primary, got %v", calls)
	}
}

func TestQueryRouted_AllReplicasDownFallsBackToPrimary(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	bad1 := &errDriver{recDriver: recDriver{name: "bad1", calls: &calls}, err: errors.New("down1")}
	bad2 := &errDriver{recDriver: recDriver{name: "bad2", calls: &calls}, err: errors.New("down2")}
	e := &Engine{drv: primary, replicas: []driver.Driver{bad1, bad2}}
	ctx := context.Background()

	if _, err := e.queryRouted(ctx, false, "SELECT 1"); err != nil {
		t.Fatalf("expected fallback to primary to succeed, got %v", err)
	}
	last := calls[len(calls)-1]
	if last != "primary:query" {
		t.Fatalf("expected primary to be the final attempt, got %v", calls)
	}
}

func TestQueryRouted_SkipsRecentlyFailedReplica(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	bad := &errDriver{recDriver: recDriver{name: "bad", calls: &calls}, err: errors.New("boom")}
	good := &recDriver{name: "good", calls: &calls}
	e := &Engine{drv: primary, replicas: []driver.Driver{bad, good}}
	ctx := context.Background()

	// First read trips the bad replica's failure window and falls through to good.
	if _, err := e.queryRouted(ctx, false, "SELECT 1"); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// Reset the recorded calls; the next read must skip the still-cooling bad replica.
	calls = calls[:0]
	if _, err := e.queryRouted(ctx, false, "SELECT 2"); err != nil {
		t.Fatalf("second read: %v", err)
	}
	for _, c := range calls {
		if c == "bad:query" {
			t.Fatalf("recently-failed replica should be skipped within cooldown, got %v", calls)
		}
	}
}

func TestQueryRouted_PrimaryFlagBypassesReplicas(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	r1 := &recDriver{name: "r1", calls: &calls}
	e := &Engine{drv: primary, replicas: []driver.Driver{r1}}
	if _, err := e.queryRouted(context.Background(), true, "SELECT 1"); err != nil {
		t.Fatalf("primary read: %v", err)
	}
	if len(calls) != 1 || calls[0] != "primary:query" {
		t.Fatalf("primary=true must hit only the primary, got %v", calls)
	}
}
