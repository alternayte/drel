package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// parsedCmd is the result of parsing a drel invocation. Handlers consume this
// instead of touching os.Args directly, so the parser is unit-testable without
// os.Exit.
type parsedCmd struct {
	Command    string   // "init" | "generate" | "migrate" | "seed" | "version" | "help"
	Subcommand string   // for "migrate": "new"|"up"|"down"|"status"|"lint"|"check"; else ""
	ConfigPath string   // resolved value of --config / -c (default "drel.yaml")
	AuthToken  string   // --auth-token (migrate subcommands only; falls back to TURSO_AUTH_TOKEN env)
	Watch      bool     // --watch / -w (generate only): run in watch mode
	Module     string   // --module: limit the work to one module of the config
	Positional []string // remaining non-flag args (e.g. the migration name)
}

// commandFlags builds a FlagSet for a (sub)command with the shared --config/-c
// flag and optional --auth-token (for migrate subcommands). Errors are
// returned, never printed, and the set never calls os.Exit.
func commandFlags(name string, withAuthToken bool, cfg *string, authToken *string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(cfg, "config", "drel.yaml", "path to drel.yaml")
	fs.StringVar(cfg, "c", "drel.yaml", "path to drel.yaml (shorthand)")
	if withAuthToken {
		fs.StringVar(authToken, "auth-token", "", "LibSQL/Turso auth token (overrides TURSO_AUTH_TOKEN env)")
	}
	return fs
}

// parseArgs parses argv (everything after the program name) into a parsedCmd.
// It uses a stdlib FlagSet per (sub)command so `--config x`, `--config=x`, and
// `-c x` all work, and `migrate new --config x name` yields Positional=["name"]
// rather than the literal "--config". Returns a usage error (never os.Exit).
func parseArgs(argv []string) (parsedCmd, error) {
	if len(argv) == 0 {
		return parsedCmd{}, fmt.Errorf("no command given")
	}

	cmd := argv[0]
	switch cmd {
	case "version":
		return parsedCmd{Command: "version"}, nil

	case "help", "--help", "-h":
		return parsedCmd{Command: "help"}, nil

	case "init", "seed":
		var cfg string
		var tok string
		fs := commandFlags(cmd, false, &cfg, &tok)
		if err := fs.Parse(argv[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return parsedCmd{Command: "help"}, nil
			}
			return parsedCmd{}, fmt.Errorf("%s: %w", cmd, err)
		}
		return parsedCmd{Command: cmd, ConfigPath: cfg, Positional: fs.Args()}, nil

	case "generate":
		var cfg string
		var tok string
		var watch bool
		var module string
		fs := commandFlags(cmd, false, &cfg, &tok)
		fs.BoolVar(&watch, "watch", false, "watch for source changes and regenerate")
		fs.BoolVar(&watch, "w", false, "watch for source changes and regenerate (shorthand)")
		fs.StringVar(&module, "module", "", "generate one module of the config only")
		if err := fs.Parse(argv[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return parsedCmd{Command: "help"}, nil
			}
			return parsedCmd{}, fmt.Errorf("%s: %w", cmd, err)
		}
		return parsedCmd{Command: cmd, ConfigPath: cfg, Watch: watch, Module: module, Positional: fs.Args()}, nil

	case "migrate":
		if len(argv) < 2 {
			return parsedCmd{}, fmt.Errorf("migrate: missing subcommand (new|up|down|status|lint|check)")
		}
		sub := argv[1]
		switch sub {
		case "--help", "-h":
			return parsedCmd{Command: "help"}, nil
		case "new", "up", "down", "status", "lint", "check":
		default:
			return parsedCmd{}, fmt.Errorf("unknown migrate command: %s", sub)
		}
		var cfg string
		var tok string
		var module string
		fs := commandFlags("migrate "+sub, true, &cfg, &tok)
		fs.StringVar(&module, "module", "", "act on one module of the config only")
		if err := fs.Parse(argv[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return parsedCmd{Command: "help"}, nil
			}
			if sub == "new" {
				return parsedCmd{}, fmt.Errorf("migrate new: invalid migration name or flag: %w", err)
			}
			return parsedCmd{}, fmt.Errorf("migrate %s: %w", sub, err)
		}
		pc := parsedCmd{Command: "migrate", Subcommand: sub, ConfigPath: cfg, AuthToken: tok, Module: module, Positional: fs.Args()}
		if sub == "new" {
			if err := validateMigrationName(pc.Positional); err != nil {
				return parsedCmd{}, err
			}
		}
		return pc, nil

	default:
		return parsedCmd{}, fmt.Errorf("unknown command: %s", cmd)
	}
}

// validateMigrationName ensures `migrate new` got exactly one positional that is
// a sane identifier (no leading dash, no path separators).
func validateMigrationName(positional []string) error {
	if len(positional) == 0 {
		return fmt.Errorf("migrate new: missing migration name")
	}
	name := positional[0]
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("migrate new: invalid migration name %q: must not start with '-' (did a flag value go missing?)", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("migrate new: invalid migration name %q: must not contain path separators", name)
	}
	return nil
}
