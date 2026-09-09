package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver"
	"github.com/alternayte/drel/internal/dsn"
	"github.com/alternayte/drel/internal/migrate"
)

// resolveAuthToken returns the auth token from parsedCmd.AuthToken (highest
// precedence) or the TURSO_AUTH_TOKEN environment variable.
func resolveAuthToken(tok string) string {
	if tok != "" {
		return tok
	}
	return os.Getenv("TURSO_AUTH_TOKEN")
}

// openMigrateDriver opens a database driver for migration commands. The dialect
// is taken from drel.yaml when present, otherwise inferred from the DSN. LibSQL/
// Turso DSNs (libsql://, wss://, https://, ...) open the libsql driver, with the
// auth token injected from parsedCmd.AuthToken / TURSO_AUTH_TOKEN.
func openMigrateDriver(ctx context.Context, parsed parsedCmd, dataSource string) (driver.Driver, error) {
	authToken := resolveAuthToken(parsed.AuthToken)
	return dsn.OpenDriver(ctx, dataSource, authToken)
}

func runMigrate(parsed parsedCmd) {
	switch parsed.Subcommand {
	case "new":
		runMigrateNew(parsed)
	case "up":
		runMigrateUp(parsed)
	case "down":
		runMigrateDown(parsed)
	case "status":
		runMigrateStatus(parsed)
	case "lint":
		runMigrateLint(parsed)
	case "check":
		runMigrateCheck(parsed)
	default:
		fmt.Fprintf(os.Stderr, "unknown migrate command: %s\n", parsed.Subcommand)
		printMigrateUsage()
		os.Exit(1)
	}
}

func printMigrateUsage() {
	fmt.Fprintln(os.Stderr, "Usage: drel migrate <command>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  new <name>    Generate a new migration from model definitions")
	fmt.Fprintln(os.Stderr, "  up            Apply all pending migrations")
	fmt.Fprintln(os.Stderr, "  down          Rollback the last applied migration")
	fmt.Fprintln(os.Stderr, "  status        Show migration status")
	fmt.Fprintln(os.Stderr, "  lint          Validate migration file checksums")
	fmt.Fprintln(os.Stderr, "  check         Fail if migration files are not yet applied to the DB")
	fmt.Fprintln(os.Stderr, "                NOTE: compares file list vs drel_migrations table only;")
	fmt.Fprintln(os.Stderr, "                does not detect out-of-band schema changes (manual ALTERs).")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  --config <path>      Path to drel.yaml (default ./drel.yaml)")
	fmt.Fprintln(os.Stderr, "  --auth-token <tok>   LibSQL/Turso auth token (or TURSO_AUTH_TOKEN env)")
}

// resolveModuleDirs returns the absolute migration directory of each module,
// or of the one module that name selects.
func resolveModuleDirs(configPath, module string) []string {
	cfg, err := codegen.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate: %v\n", err)
		os.Exit(1)
	}

	mods := cfg.ModuleList()
	if module != "" {
		one, err := cfg.Module(module)
		if err != nil {
			fmt.Fprintf(os.Stderr, "drel migrate: %v\n", err)
			os.Exit(1)
		}
		mods = []codegen.ModuleConfig{one}
	}

	cfgDir, _ := filepath.Abs(filepath.Dir(configPath))
	dirs := make([]string, 0, len(mods))
	for _, m := range mods {
		dir := m.Migrations
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cfgDir, dir)
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

// migrationFS returns one filesystem for each migration directory that exists.
// A module that has no migration directory yet contributes nothing.
func migrationFS(dirs []string) []fs.FS {
	var out []fs.FS
	for _, d := range dirs {
		if _, err := os.Stat(d); err != nil {
			continue
		}
		out = append(out, os.DirFS(d))
	}
	return out
}

// newMigrateRunner builds a runner over every migration directory of the
// selected modules. The sets merge in version order, so the migrations of two
// slices run in the order they were written.
func newMigrateRunner(drv driver.Driver, parsed parsedCmd, dsn string) *migrate.Runner {
	dirs := resolveModuleDirs(parsed.ConfigPath, parsed.Module)
	dialect := runnerDialect(parsed.ConfigPath, dsn)
	if len(dirs) == 1 {
		return migrate.NewRunner(drv, dirs[0], dialect)
	}
	return migrate.NewRunnerFS(drv, dialect, migrationFS(dirs)...)
}

// resolveNewMigrationDir returns the single directory that `migrate new` writes
// into. A config with more than one module must name the module, because the
// migration belongs to exactly one slice.
func resolveNewMigrationDir(configPath, module string) (codegen.ModuleConfig, string) {
	cfg, err := codegen.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
		os.Exit(1)
	}

	mods := cfg.ModuleList()
	var target codegen.ModuleConfig
	switch {
	case module != "":
		target, err = cfg.Module(module)
		if err != nil {
			fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
			os.Exit(1)
		}
	case len(mods) == 1:
		target = mods[0]
	default:
		names := make([]string, len(mods))
		for i, m := range mods {
			names[i] = m.Name
		}
		fmt.Fprintf(os.Stderr,
			"drel migrate new: the config declares %d modules; name the one this migration belongs to with --module (declared: %s)\n",
			len(mods), strings.Join(names, ", "))
		os.Exit(1)
	}

	dir := target.Migrations
	if !filepath.IsAbs(dir) {
		cfgDir, _ := filepath.Abs(filepath.Dir(configPath))
		dir = filepath.Join(cfgDir, dir)
	}
	return target, dir
}

func requireDSN() string {
	dataSource := os.Getenv("DATABASE_URL")
	if dataSource == "" {
		fmt.Fprintln(os.Stderr, "drel migrate: DATABASE_URL environment variable is required")
		os.Exit(1)
	}
	return dataSource
}

// runnerDialect resolves the dialect string for the Runner: config dialect when
// set, else DSN inference.
func runnerDialect(configPath, dataSource string) string {
	if cfg, err := codegen.LoadConfig(configPath); err == nil && cfg.Dialect != "" {
		// drel.yaml uses "sqlite" for libsql; re-detect so libsql is distinguished
		// for lock selection.
		if d := dsn.DetectDialect(dataSource); d == "libsql" {
			return "libsql"
		}
		return cfg.Dialect
	}
	return dsn.DetectDialect(dataSource)
}

func runMigrateNew(parsed parsedCmd) {
	if len(parsed.Positional) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: drel migrate new <name>")
		os.Exit(1)
	}
	name := parsed.Positional[0]
	cp := parsed.ConfigPath

	cfg, err := codegen.LoadConfig(cp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
		os.Exit(1)
	}

	// The migration belongs to exactly one module, and it diffs that module's
	// own snapshot. A slice therefore owns its migrations.
	target, mDir := resolveNewMigrationDir(cp, parsed.Module)

	cfgDir, _ := filepath.Abs(filepath.Dir(cp))
	models, err := codegen.ScanPackages(target.Packages, cfgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
		os.Exit(1)
	}
	if len(models) == 0 {
		fmt.Fprintf(os.Stderr, "drel migrate new: no models found in module %q\n", target.Name)
		os.Exit(1)
	}
	// The migration path must enforce the same limits as `drel generate`.
	// Without this a rejected model still reaches BuildSchema and emits DDL
	// that is silently wrong, such as a single-column REFERENCES to a
	// composite-key table.
	if err := codegen.ValidateModels(models); err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
		os.Exit(1)
	}

	// A model may reference a table of another module. The merged apply order
	// is by timestamp, so warn that the order matters.
	warnCrossModuleRefs(cfg, target, models, cfgDir)

	dialect := cfg.Dialect

	// Build the desired logical schema and compare against the persisted snapshot.
	desired := codegen.BuildSchema(models, dialect)
	snapshotPath := filepath.Join(mDir, ".drel_snapshot.json")
	old, hasSnapshot, err := codegen.LoadSnapshot(snapshotPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
		os.Exit(1)
	}

	existing, _ := migrate.ParseMigrationDir(mDir)

	var upSQL, downSQL string
	if !hasSnapshot {
		if len(existing) > 0 {
			// Legacy project adopting snapshots: seed the snapshot from current
			// models without generating a migration.
			if err := codegen.SaveSnapshot(snapshotPath, desired); err != nil {
				fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("drel: initialized schema snapshot from current models (no migration generated); re-run after changing models")
			return
		}
		// First migration: emit the full schema as up. The down is the complete
		// reverse — drop every table (pivots included, in dependency order) and
		// every enum type — derived by diffing the desired schema against an empty
		// one so pivots and enum types are covered (GenerateDropSchema drops only
		// model tables, leaking pivots and enums on rollback).
		upSQL = codegen.GenerateSchema(models, dialect)
		dropUp, _, dErr := codegen.DiffSchemas(desired, codegen.Schema{}, dialect)
		if dErr != nil {
			fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", dErr)
			os.Exit(1)
		}
		downSQL = dropUp
	} else {
		// Incremental migration: structured diff of snapshot against desired schema.
		var dErr error
		upSQL, downSQL, dErr = codegen.DiffSchemas(old, desired, dialect)
		if dErr != nil {
			fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", dErr)
			os.Exit(1)
		}
		if upSQL == "" && downSQL == "" {
			fmt.Println("drel: no schema changes detected")
			return
		}
	}

	if len(existing) > 0 {
		fmt.Fprintln(os.Stderr, "drel: tip: run `drel migrate check` to confirm all prior migrations are applied before deploying")
	}

	version, err := migrate.WriteMigration(mDir, name, upSQL, downSQL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
		os.Exit(1)
	}

	// Persist the snapshot only after the migration is successfully written.
	if err := codegen.SaveSnapshot(snapshotPath, desired); err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
		os.Exit(1)
	}

	// Refresh the embed file so the new migration reaches the host through
	// Engine.ApplyMigrationsFS.
	if _, err := codegen.WriteMigrationsEmbed(mDir, target.Name); err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate new: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("drel: created migration %s_%s in module %s\n", version, name, target.Name)
}

// warnCrossModuleRefs reports a relation that points at a table owned by
// another module. The reference is allowed, and the merged migrations apply in
// timestamp order, so a person must know that the order matters.
func warnCrossModuleRefs(cfg *codegen.Config, target codegen.ModuleConfig, models []codegen.ModelInfo, cfgDir string) {
	others := make(map[string]string) // model name -> module name
	for _, m := range cfg.ModuleList() {
		if m.Name == target.Name {
			continue
		}
		otherModels, err := codegen.ScanPackages(m.Packages, cfgDir)
		if err != nil {
			continue // a module that does not build cannot be checked here
		}
		for _, om := range otherModels {
			others[om.Name] = m.Name
		}
	}
	if len(others) == 0 {
		return
	}

	own := make(map[string]bool)
	for _, m := range models {
		own[m.Name] = true
	}

	seen := make(map[string]bool)
	for _, m := range models {
		for _, f := range m.Fields {
			if f.Relation == nil || f.Relation.TargetModel == "" {
				continue
			}
			ownerModule, ok := others[f.Relation.TargetModel]
			if !ok || own[f.Relation.TargetModel] {
				continue
			}
			key := f.Relation.TargetModel + "/" + ownerModule
			if seen[key] {
				continue
			}
			seen[key] = true
			fmt.Fprintf(os.Stderr,
				"drel: warning: module %q references model %q owned by module %q; the merged migrations apply in timestamp order, so generate %q first\n",
				target.Name, f.Relation.TargetModel, ownerModule, ownerModule)
		}
	}
}

func runMigrateUp(parsed parsedCmd) {
	dsn := requireDSN()
	ctx, stop := signalContext()
	defer stop()

	drv, err := openMigrateDriver(ctx, parsed, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate up: %v\n", err)
		os.Exit(1)
	}
	defer drv.Close()

	runner := newMigrateRunner(drv, parsed, dsn)
	count, err := runner.Up(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate up: %v\n", err)
		os.Exit(1)
	}
	if count == 0 {
		fmt.Println("drel: no pending migrations")
	} else {
		fmt.Printf("drel: applied %d migration(s)\n", count)
	}
}

func runMigrateDown(parsed parsedCmd) {
	dsn := requireDSN()
	ctx, stop := signalContext()
	defer stop()

	drv, err := openMigrateDriver(ctx, parsed, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate down: %v\n", err)
		os.Exit(1)
	}
	defer drv.Close()

	runner := newMigrateRunner(drv, parsed, dsn)
	if err := runner.Down(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate down: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("drel: rolled back last migration")
}

func runMigrateStatus(parsed parsedCmd) {
	dsn := requireDSN()
	ctx, stop := signalContext()
	defer stop()

	drv, err := openMigrateDriver(ctx, parsed, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate status: %v\n", err)
		os.Exit(1)
	}
	defer drv.Close()

	runner := newMigrateRunner(drv, parsed, dsn)
	statuses, err := runner.Status(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate status: %v\n", err)
		os.Exit(1)
	}
	if len(statuses) == 0 {
		fmt.Println("No migrations found")
		return
	}
	for _, s := range statuses {
		var marker string
		switch s.State {
		case migrate.StateApplied:
			marker = "[x]"
		case migrate.StatePending:
			marker = "[ ]"
		case migrate.StateModified:
			marker = "[!]"
		case migrate.StateMissing:
			marker = "[?]"
		default:
			marker = "[ ]"
		}
		label := string(s.State)
		fmt.Printf("  %s  %s_%s  (%s)\n", marker, s.Version, s.Name, label)
	}
}

func runMigrateLint(parsed parsedCmd) {
	dsn := requireDSN()
	ctx, stop := signalContext()
	defer stop()

	drv, err := openMigrateDriver(ctx, parsed, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate lint: %v\n", err)
		os.Exit(1)
	}
	defer drv.Close()

	runner := newMigrateRunner(drv, parsed, dsn)
	issues, err := runner.Lint(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate lint: %v\n", err)
		os.Exit(1)
	}
	if len(issues) == 0 {
		fmt.Println("drel: all migration checksums valid")
		return
	}
	for _, issue := range issues {
		fmt.Fprintf(os.Stderr, "  MODIFIED  %s_%s (checksum mismatch)\n", issue.Version, issue.Name)
	}
	os.Exit(1)
}

func runMigrateCheck(parsed parsedCmd) {
	dsn := requireDSN()
	ctx, stop := signalContext()
	defer stop()

	drv, err := openMigrateDriver(ctx, parsed, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate check: %v\n", err)
		os.Exit(1)
	}
	defer drv.Close()

	runner := newMigrateRunner(drv, parsed, dsn)
	pending, err := runner.Pending(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel migrate check: %v\n", err)
		os.Exit(1)
	}
	if len(pending) == 0 {
		// NOTE: "no unapplied files" is not the same as "no schema drift" —
		// manual out-of-band ALTERs are not detected here. Use `migrate lint`
		// to catch checksum tampering; live schema comparison requires an
		// external introspection tool.
		fmt.Println("drel: no unapplied migrations")
		return
	}
	fmt.Fprintf(os.Stderr, "drel: %d unapplied migration(s); run `drel migrate up` before generating new migrations to avoid snapshot drift:\n", len(pending))
	for _, m := range pending {
		fmt.Fprintf(os.Stderr, "  [ ] %s_%s\n", m.Version, m.Name)
	}
	os.Exit(1)
}
