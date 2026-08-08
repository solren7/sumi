package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The layering rules from CLAUDE.md, enforced instead of merely documented. This
// is the cheapest useful piece of an onion architecture: a checkable dependency
// direction. It needs no database, so it runs in every `go test ./...`.
//
// Non-test files only: tests legitimately reach for lower layers to set up
// fixtures (api_test.go builds a dbgen.Queries).

const modulePath = "sumi"

type rule struct {
	// layer is a path prefix relative to the repo root.
	layer string
	// deny lists import prefixes that layer must not use.
	deny []string
	// allow carves out exceptions checked before deny.
	allow []string
	why   string
}

var rules = []rule{
	{
		layer: "internal/handlers",
		deny:  []string{modulePath + "/internal/repository"},
		why: "handlers must consume the domain types a service returns, not database rows; " +
			"otherwise a schema change reaches the HTTP layer (see internal/services/models.go)",
	},
	{
		layer: "internal/domain",
		deny: []string{
			modulePath + "/internal/repository",
			modulePath + "/internal/database",
			modulePath + "/internal/cache",
			modulePath + "/internal/services",
			modulePath + "/internal/handlers",
			modulePath + "/internal/apps",
			modulePath + "/middleware",
			modulePath + "/config",
			"github.com/jackc/pgx",
			"github.com/redis/go-redis",
			"github.com/gofiber/fiber",
			"github.com/spf13/cobra",
			"golang.org/x/crypto",
		},
		why: "the domain layer must stay infrastructure-free so its rules can be unit-tested " +
			"without a database; pkg/errorx and shopspring/decimal are allowed as pure value types",
	},
	{
		layer: "internal/csvimport",
		deny: []string{
			modulePath + "/internal/repository",
			modulePath + "/internal/database",
			modulePath + "/internal/cache",
			modulePath + "/internal/services",
			modulePath + "/internal/handlers",
			modulePath + "/internal/apps",
			modulePath + "/config",
			"github.com/jackc/pgx",
			"github.com/redis/go-redis",
			"github.com/gofiber/fiber",
			"github.com/spf13/cobra",
			"os",
			"net/http",
		},
		why: "CSV parsing is pure conversion: it takes bytes and returns records, so it " +
			"must not read files or reach the network itself — that keeps every format " +
			"quirk unit-testable",
	},
	{
		layer: "internal/services",
		deny: []string{
			modulePath + "/internal/handlers",
			modulePath + "/internal/apps",
			modulePath + "/middleware",
			"github.com/gofiber/fiber",
		},
		why: "services must not depend on the transport layer; dependencies point inwards only",
	},
	{
		layer: "internal/repository",
		deny: []string{
			modulePath + "/internal/services",
			modulePath + "/internal/handlers",
			modulePath + "/internal/domain",
			modulePath + "/middleware",
		},
		why: "generated repository code sits at the bottom and must not reach upwards",
	},
	{
		layer: "cmd",
		deny:  []string{modulePath + "/internal/services", modulePath + "/internal/repository"},
		allow: []string{modulePath + "/internal/apps"},
		why: "the CLI is an HTTP client of the API; it must not link the service layer directly, " +
			"apart from cmd/api.go starting the server via internal/apps",
	},
	{
		layer: "middleware",
		deny:  []string{modulePath + "/internal/handlers", modulePath + "/internal/repository"},
		why:   "middleware is used by handlers, so depending on them would be a cycle",
	},
}

func TestLayerDependencies(t *testing.T) {
	for _, r := range rules {
		t.Run(r.layer, func(t *testing.T) {
			files := goFilesUnder(t, r.layer)
			if len(files) == 0 {
				t.Fatalf("no non-test Go files found under %s; is the rule stale?", r.layer)
			}

			for _, file := range files {
				for _, imported := range importsOf(t, file) {
					if matchesAny(imported, r.allow) {
						continue
					}
					if prefix, hit := firstMatch(imported, r.deny); hit {
						t.Errorf("%s imports %q (forbidden prefix %q)\n  why: %s",
							file, imported, prefix, r.why)
					}
				}
			}
		})
	}
}

// TestDomainHasNoModuleDependencies is the stricter half of the domain rule: it
// must not import any other package of this module except pkg/, so the layer
// cannot start pulling in application concerns.
func TestDomainHasNoModuleDependencies(t *testing.T) {
	for _, dir := range []string{"internal/domain", "internal/csvimport"} {
		assertNoModuleDependencies(t, dir)
	}
}

func assertNoModuleDependencies(t *testing.T, dir string) {
	t.Helper()
	for _, file := range goFilesUnder(t, dir) {
		for _, imported := range importsOf(t, file) {
			if !strings.HasPrefix(imported, modulePath+"/") {
				continue // standard library or third-party, covered by the deny list
			}
			if strings.HasPrefix(imported, modulePath+"/pkg/") {
				continue // pkg/errorx and friends are leaf utilities
			}
			t.Errorf("%s imports %q; this layer may only use %s/pkg/... internally",
				file, imported, modulePath)
		}
	}
}

func goFilesUnder(t *testing.T, dir string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return files
}

func importsOf(t *testing.T, file string) []string {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	imports := make([]string, 0, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("%s: bad import literal %s", file, spec.Path.Value)
		}
		imports = append(imports, path)
	}
	return imports
}

func matchesAny(value string, prefixes []string) bool {
	_, hit := firstMatch(value, prefixes)
	return hit
}

func firstMatch(value string, prefixes []string) (string, bool) {
	for _, prefix := range prefixes {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return prefix, true
		}
	}
	return "", false
}
