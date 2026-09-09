package codegen

import (
	"path/filepath"
	"strings"
)

// assignModules tags each model with the module that owns its directory. A
// model whose directory sits under one of a module's package patterns belongs
// to that module.
//
// The longest matching prefix wins, so a nested slice inside another slice's
// tree is assigned to the nested one.
func assignModules(models []ModelInfo, mods []ModuleConfig, cfgDir string) {
	type prefix struct {
		dir    string
		module string
	}
	var prefixes []prefix

	for _, m := range mods {
		for _, p := range m.Packages {
			dir := strings.TrimSuffix(p, "/...")
			dir = strings.TrimSuffix(dir, "/")
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(cfgDir, dir)
			}
			prefixes = append(prefixes, prefix{dir: filepath.Clean(dir), module: m.Name})
		}
	}

	for i := range models {
		dir := filepath.Clean(models[i].Dir)
		best := ""
		bestLen := -1
		for _, p := range prefixes {
			if dir != p.dir && !strings.HasPrefix(dir, p.dir+string(filepath.Separator)) {
				continue
			}
			if len(p.dir) > bestLen {
				best, bestLen = p.module, len(p.dir)
			}
		}
		models[i].Module = best
	}
}
