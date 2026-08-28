package runtimeext

import (
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeextProductionFilesArePublicLeafContracts(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, currentPackage := range packages {
		for filename, file := range currentPackage.Files {
			for _, spec := range file.Imports {
				path, unquoteErr := strconv.Unquote(spec.Path.Value)
				if unquoteErr != nil {
					t.Errorf("%s has invalid import %s", filename, spec.Path.Value)
					continue
				}
				if strings.Contains(path, "/internal/") || path == "database/sql" || path == "net/http" {
					t.Errorf("public runtimeext contract leaks forbidden implementation import %q from %s", path, filename)
				}
			}
		}
	}
}
