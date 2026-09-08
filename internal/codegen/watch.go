package codegen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// watchDirs returns the deduped, sorted set of filesystem directories that back
// the models referenced by the config at configPath. These are the directories
// the watcher polls for .go changes. It mirrors Generate's config-dir resolution
// so patterns resolve identically.
func watchDirs(configPath string) ([]string, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	cfgDir := filepath.Dir(configPath)
	if !filepath.IsAbs(cfgDir) {
		abs, err := filepath.Abs(cfgDir)
		if err != nil {
			return nil, fmt.Errorf("codegen: resolve config dir: %w", err)
		}
		cfgDir = abs
	}

	// Patterns are resolved relative to the config directory, matching Generate.
	models, err := ScanPackages(cfg.Packages, cfgDir)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var dirs []string
	for _, m := range models {
		if m.Dir == "" || seen[m.Dir] {
			continue
		}
		seen[m.Dir] = true
		dirs = append(dirs, m.Dir)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// dirsSignature returns a deterministic fingerprint of the .go source files
// directly under the given dirs (non-recursive — one dir per package). Generated
// drel files (*_drel.go) and any basename in skip (the DB output file) are
// excluded so regeneration never changes the signature and the watcher does not
// self-trigger. The fingerprint folds path, modtime (UnixNano), and size.
func dirsSignature(dirs []string, skip map[string]bool) (string, error) {
	var b strings.Builder
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", fmt.Errorf("codegen: watch read dir %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".go") {
				continue
			}
			if strings.HasSuffix(name, "_drel.go") || skip[name] {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return "", fmt.Errorf("codegen: watch stat %s: %w", name, err)
			}
			b.WriteString(filepath.Join(dir, name))
			b.WriteByte(0)
			b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
			b.WriteByte(0)
			b.WriteString(strconv.FormatInt(info.Size(), 10))
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

// GenerateWatch runs Generate once, then re-runs it whenever a .go source file
// under the scanned package directories changes. It blocks until ctx is
// cancelled, at which point it returns nil. Generated *_drel.go files and the DB
// output file are ignored when detecting changes so regeneration never
// self-triggers. pollInterval controls the mtime poll cadence; pass 0 for the
// default (500ms). Watch uses only the standard library (no fsnotify) to honour
// the zero-runtime-dependency rule.
func GenerateWatch(ctx context.Context, configPath string, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}

	// Resolve watch dirs and skip set cheaply (no go/packages.Load) so we can
	// capture the baseline signature BEFORE the initial Generate. Any changes
	// made while Generate runs are thus visible on the first poll tick.
	skip, dirs, err := quickWatchDirs(configPath)
	if err != nil {
		return err
	}

	last, err := dirsSignature(dirs, skip)
	if err != nil {
		return err
	}

	// Initial generation. A failure here is reported but does not abort the
	// watch loop — the developer can fix the source and the next poll recovers.
	if err := Generate(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "drel watch: %v\n", err)
	} else {
		fmt.Println("drel watch: generated; watching for changes (Ctrl+C to stop)")
	}

	// Recompute signature after initial regen to exclude any files that were
	// freshly written by Generate itself (not generated files, but new source
	// files, etc.). This avoids a spurious first-tick regen in edge cases.
	skip, dirs, err = quickWatchDirs(configPath)
	if err != nil {
		return err
	}
	if sig, err := dirsSignature(dirs, skip); err == nil {
		last = sig
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Re-resolve dirs each tick so newly-added packages in drel.yaml
			// are picked up; cheap (no go/packages.Load).
			skip, dirs, err = quickWatchDirs(configPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "drel watch: %v\n", err)
				continue
			}
			sig, err := dirsSignature(dirs, skip)
			if err != nil {
				fmt.Fprintf(os.Stderr, "drel watch: %v\n", err)
				continue
			}
			if sig == last {
				continue
			}
			last = sig
			if err := Generate(configPath); err != nil {
				fmt.Fprintf(os.Stderr, "drel watch: %v\n", err)
				continue
			}
			fmt.Println("drel watch: regenerated")
			// Recompute the signature after regen so the freshly-written
			// (non-generated) files do not look "changed" next tick.
			if sig, err := dirsSignature(dirs, skip); err == nil {
				last = sig
			}
		}
	}
}

// quickWatchDirs resolves the DB-output basename to skip and the filesystem
// directories corresponding to the config's package patterns, without invoking
// go/packages.Load (cheap, suitable for the poll tick). It resolves each pattern
// relative to the module root, mirroring how Generate resolves them.
func quickWatchDirs(configPath string) (skip map[string]bool, dirs []string, err error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	skip = map[string]bool{filepath.Base(cfg.Output.DB): true}

	cfgDir := filepath.Dir(configPath)
	if !filepath.IsAbs(cfgDir) {
		abs, absErr := filepath.Abs(cfgDir)
		if absErr != nil {
			return nil, nil, fmt.Errorf("codegen: resolve config dir: %w", absErr)
		}
		cfgDir = abs
	}

	seen := make(map[string]bool)
	for _, pat := range cfg.Packages {
		// Patterns are resolved relative to the config directory, matching Generate.
		d := filepath.Join(cfgDir, pat)
		if seen[d] {
			continue
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return skip, dirs, nil
}
