// Package arch holds architecture tests that enforce the layer dependency rule
// (DEVELOPMENT_RULE §5.1). It contains no production code.
package arch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePrefix = "github.com/ahdirmai/jg-smm/apps/api/internal/"

// TestLayerDependencies enforces that inner layers do not import outer ones.
func TestLayerDependencies(t *testing.T) {
	// For each layer, the set of internal packages it must NOT import.
	forbidden := map[string][]string{
		// domain is the innermost layer: no internal imports at all.
		"domain": {""},
		// service depends on port/domain only.
		"service": {"repository", "adapter", "http", "scheduler", "config"},
		// port must not depend on implementations or transports.
		"port": {"repository", "adapter", "http", "scheduler", "service", "config"},
		// repository implements ports; must not import transport or adapters.
		"repository": {"http", "adapter", "scheduler", "config"},
	}

	for layer, bads := range forbidden {
		dir := layerDir(layer)
		if dir == "" {
			continue // layer not present yet
		}
		t.Run(layer, func(t *testing.T) {
			err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
					return err
				}
				if strings.HasSuffix(path, "_test.go") {
					return nil
				}
				imports, err := importsOf(path)
				if err != nil {
					return err
				}
				for _, imp := range imports {
					if !strings.HasPrefix(imp, modulePrefix) {
						continue
					}
					rel := strings.TrimPrefix(imp, modulePrefix)
					pkg := strings.Split(rel, "/")[0]
					if pkg == layer {
						continue
					}
					for _, bad := range bads {
						if bad == "" || pkg == bad {
							t.Errorf("%s imports %s (layer rule: %s must not import %s)", path, imp, layer, bad)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("walk: %v", err)
			}
		})
	}
}

func layerDir(layer string) string {
	dir := filepath.Join("..", layer)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir
	}
	return ""
}

func importsOf(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var imports []string
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		imports = append(imports, p)
	}
	return imports, nil
}
