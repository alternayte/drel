package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/alternayte/drel/internal/codegen"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	parsed, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel: %v\n", err)
		printUsage()
		os.Exit(1)
	}

	switch parsed.Command {
	case "init":
		runInit(parsed)
	case "generate":
		runGenerate(parsed)
	case "migrate":
		runMigrate(parsed)
	case "seed":
		runSeed(parsed)
	case "version":
		fmt.Printf("drel %s (%s)\n", version, commit)
	case "help":
		printUsageStdout()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", parsed.Command)
		printUsage()
		os.Exit(1)
	}
}

func fprintUsage(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintln(w, "Usage: drel <command>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init        Scaffold a drel.yaml configuration file")
	fmt.Fprintln(w, "  generate    Generate code from model definitions (--watch for inner loop,")
	fmt.Fprintln(w, "              --module <name> for one feature slice)")
	fmt.Fprintln(w, "  migrate     Manage database migrations")
	fmt.Fprintln(w, "  seed        Run seed functions against the database")
	fmt.Fprintln(w, "  version     Print version")
	fmt.Fprintln(w, "  help        Show this help")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --config, -c <path>   Path to drel.yaml (default: drel.yaml)")
}

// printUsage prints usage to stderr (for error paths).
func printUsage() { fprintUsage(os.Stderr) }

// printUsageStdout prints usage to stdout (for --help/-h/help, exit 0).
func printUsageStdout() { fprintUsage(os.Stdout) }

const defaultConfig = `# drel configuration — see https://github.com/alternayte/drel
#
# To run codegen with ` + "`go generate ./...`" + `, add this directive to a
# top-level .go file (e.g. tools.go or your main package):
#
#     //go:generate drel generate
#
packages:
  - ./features/users
  # - ./features/posts

output:
  db: ./db/drel_gen.go          # aggregated DB struct
  migrations: ./db/migrations   # SQL migration files

dialect: postgres               # postgres | sqlite

# A project of feature slices declares modules instead of packages. Each module
# owns its models and its migrations, so deleting the directory removes the
# feature. Generate one slice with ` + "`drel generate --module posts`" + `, and write
# its migration with ` + "`drel migrate new --module posts <name>`" + `.
#
# modules:
#   - name: users
#     packages: [./internal/features/users]
#     migrations: ./internal/features/users/migrations
#   - name: posts
#     packages: [./internal/features/posts]
#
# Each module gets a generated migrations_drel.go that embeds its SQL. Apply
# them together:
#
#     n, err := db.ApplyMigrationsFS(ctx, users.FS, posts.FS)
`

// initGoGenerateHint returns the post-init instruction telling the user how to
// enable `go generate ./...` support via the //go:generate directive.
func initGoGenerateHint() string {
	return "drel: to enable `go generate ./...`, add this line to a top-level .go file:\n" +
		"\n" +
		"    //go:generate drel generate\n"
}

func runInit(parsed parsedCmd) {
	configPath := parsed.ConfigPath

	if _, err := os.Stat(configPath); err == nil {
		fmt.Fprintf(os.Stderr, "drel init: %s already exists; not overwriting\n", configPath)
		os.Exit(1)
	}

	if err := os.WriteFile(configPath, []byte(defaultConfig), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "drel init: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("drel: wrote %s\n", configPath)
	fmt.Println("drel: edit the packages list, then run `drel generate`")
	fmt.Print(initGoGenerateHint())
}

func runSeed(parsed parsedCmd) {
	cfg, err := codegen.LoadConfig(parsed.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drel seed: %v\n", err)
		os.Exit(1)
	}
	if cfg.Seed == "" {
		fmt.Fprintln(os.Stderr, "drel seed: no `seed:` package configured in drel.yaml")
		fmt.Fprintln(os.Stderr, "  Add e.g. `seed: ./db/seed` pointing at a Go main that seeds the database.")
		os.Exit(1)
	}

	ctx, stop := signalContext()
	defer stop()

	cmd := exec.CommandContext(ctx, "go", "run", cfg.Seed)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "drel seed: %v\n", err)
		os.Exit(1)
	}
}

// signalContext returns a context cancelled on SIGINT/SIGTERM plus its stop fn.
// Callers must defer stop() to release the signal handler.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// parseGenerateFlags parses the args after "generate" into a config path and a
// watch flag, supporting both `--config x`/`--config=x` and `-w`/`--watch`.
// It delegates to parseArgs so flag semantics are consistent with the rest of
// the CLI; it exists as a thin shim so the flag contract can be unit-tested
// without exec-ing the binary.
func parseGenerateFlags(argv []string) (configPath string, watch bool) {
	full := append([]string{"generate"}, argv...)
	parsed, err := parseArgs(full)
	if err != nil {
		return "drel.yaml", false
	}
	return parsed.ConfigPath, parsed.Watch
}

func runGenerate(parsed parsedCmd) {
	if parsed.Watch {
		ctx, stop := signalContext()
		defer stop()
		if err := codegen.GenerateWatch(ctx, parsed.ConfigPath, 0); err != nil {
			fmt.Fprintf(os.Stderr, "drel generate: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("drel: watch stopped")
		return
	}

	if err := codegen.GenerateModule(parsed.ConfigPath, parsed.Module); err != nil {
		fmt.Fprintf(os.Stderr, "drel generate: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("drel: code generation complete")
}
