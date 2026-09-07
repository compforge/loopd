package runtime

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// +case=`Runtime layers share model types without importing the root facade or reversing their dependency direction.`
func TestLayerDependencies(t *testing.T) {
	const root = "github.com/compforge/loopd/runtime"
	allowed := map[string]map[string]bool{
		"verb":    {"service": true, "model": true},
		"service": {"infra": true, "model": true},
		"infra":   {"model": true},
		"model":   {},
	}
	for layer, dependencies := range allowed {
		entries, err := os.ReadDir(layer)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(layer, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if importPath == root {
					t.Errorf("%s imports root runtime facade", path)
				} else if strings.HasPrefix(importPath, root+"/") {
					target := strings.Split(strings.TrimPrefix(importPath, root+"/"), "/")[0]
					if !dependencies[target] {
						t.Errorf("%s imports disallowed layer %s", path, target)
					}
				}
			}
		}
	}
}
