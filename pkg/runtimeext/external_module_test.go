package runtimeext_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRuntimeextCompilesFromOutsideTheRepositoryModule(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current file")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	externalRoot := t.TempDir()
	goMod := []byte("module example.com/domainry-project\n\ngo 1.26.0\n\nrequire github.com/domainry/domainry-runtime v0.0.0\n\nreplace github.com/domainry/domainry-runtime => " + repositoryRoot + "\n")
	if err := os.WriteFile(filepath.Join(externalRoot, "go.mod"), goMod, 0o600); err != nil {
		t.Fatal(err)
	}
	source := []byte(`package project

import (
	"context"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

type BookClassCapabilities struct{}
type BookClassInput struct{}
type BookClassResult struct{}

var BookClass runtimeext.Handler[BookClassCapabilities, BookClassInput, BookClassResult] =
	func(context.Context, BookClassCapabilities, BookClassInput) (BookClassResult, error) {
		return BookClassResult{}, nil
	}

func Extensions(handler runtimeext.BusinessHandler) runtimeext.ProjectExtensions {
	return runtimeext.ProjectExtensions{BusinessHandlers: []runtimeext.BusinessHandler{handler}}
}

func FrozenRegistry(handler runtimeext.BusinessHandler) (*runtimeext.ProjectExtensionRegistry, error) {
	registry := runtimeext.NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		return nil, err
	}
	registry.Freeze()
	if _, found := registry.BusinessHandlerBinding(handler.Descriptor().ActionKey); !found {
		panic("registered Action binding is missing")
	}
	return registry, nil
}
`)
	if err := os.WriteFile(filepath.Join(externalRoot, "project.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = externalRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("external project cannot compile runtimeext: %v\n%s", err, output)
	}
}
