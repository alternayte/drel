package codegen

import (
	"fmt"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultModuleName is the name of the single implicit module that a config
// without a modules block describes.
const DefaultModuleName = "default"

// ModuleConfig describes one feature slice. A slice owns its models and its
// migrations, so a person can delete its directory and remove the feature.
type ModuleConfig struct {
	Name       string   `yaml:"name"`
	Packages   []string `yaml:"packages"`
	Migrations string   `yaml:"migrations"`
}

type Config struct {
	Packages []string       `yaml:"packages"`
	Modules  []ModuleConfig `yaml:"modules"`
	Output   OutputConfig   `yaml:"output"`
	Dialect  string         `yaml:"dialect"`
	// Seed is an optional path to a Go main package that seeds the database.
	// `drel seed` runs it with `go run`, passing through DATABASE_URL.
	Seed string `yaml:"seed"`
}

type OutputConfig struct {
	DB         string `yaml:"db"`
	Migrations string `yaml:"migrations"`
}

// validDialects is the closed set of code-generation dialects. libSQL is not a
// codegen target; it reuses the SQLite dialect at runtime, so it is excluded here.
var validDialects = map[string]bool{"postgres": true, "sqlite": true}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("codegen: read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("codegen: parse config %s: %w", path, err)
	}
	if len(cfg.Packages) > 0 && len(cfg.Modules) > 0 {
		return nil, fmt.Errorf("codegen: config %s: set packages or modules, not both", path)
	}
	if len(cfg.Packages) == 0 && len(cfg.Modules) == 0 {
		return nil, fmt.Errorf("codegen: config %s: no packages specified", path)
	}

	seen := make(map[string]bool)
	for i, m := range cfg.Modules {
		if m.Name == "" {
			return nil, fmt.Errorf("codegen: config %s: module %d has no name", path, i+1)
		}
		if len(m.Packages) == 0 {
			return nil, fmt.Errorf("codegen: config %s: module %q lists no packages", path, m.Name)
		}
		if seen[m.Name] {
			return nil, fmt.Errorf("codegen: config %s: module %q is declared two times", path, m.Name)
		}
		seen[m.Name] = true
	}
	if cfg.Output.DB == "" {
		cfg.Output.DB = "./db/drel_gen.go"
	}
	if cfg.Output.Migrations == "" {
		cfg.Output.Migrations = "./db/migrations"
	}
	if cfg.Dialect == "" {
		cfg.Dialect = "postgres"
	}
	if !validDialects[cfg.Dialect] {
		return nil, fmt.Errorf("codegen: config %s: unknown dialect %q (valid: postgres, sqlite)", path, cfg.Dialect)
	}
	return &cfg, nil
}

// ModuleList returns the modules of the config. A config that lists packages
// instead of modules describes one module named "default", so every caller
// works with one shape.
func (c *Config) ModuleList() []ModuleConfig {
	if len(c.Modules) == 0 {
		return []ModuleConfig{{
			Name:       DefaultModuleName,
			Packages:   c.Packages,
			Migrations: c.Output.Migrations,
		}}
	}

	out := make([]ModuleConfig, len(c.Modules))
	for i, m := range c.Modules {
		if m.Migrations == "" {
			// A slice keeps its migrations inside its own directory, so
			// deleting the directory removes the feature completely.
			m.Migrations = path.Join(m.Packages[0], "migrations")
			if strings.HasPrefix(m.Packages[0], "./") {
				m.Migrations = "./" + m.Migrations
			}
		}
		out[i] = m
	}
	return out
}

// Module returns one module by name. The error lists the names that exist.
func (c *Config) Module(name string) (ModuleConfig, error) {
	mods := c.ModuleList()
	var names []string
	for _, m := range mods {
		if m.Name == name {
			return m, nil
		}
		names = append(names, m.Name)
	}
	return ModuleConfig{}, fmt.Errorf("codegen: unknown module %q (declared: %s)", name, strings.Join(names, ", "))
}

// AllPackages returns every package of every module. The aggregated DB struct
// needs all of them, even when generation targets one module.
func (c *Config) AllPackages() []string {
	var out []string
	for _, m := range c.ModuleList() {
		out = append(out, m.Packages...)
	}
	return out
}
