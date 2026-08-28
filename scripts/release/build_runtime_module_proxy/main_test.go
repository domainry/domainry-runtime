package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/module"
)

func TestPublishContainsOnlyRuntimeBuildClosureAndCompilesConsumer(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	repository := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
	proxy := filepath.Join(t.TempDir(), "proxy")
	result, err := publish(repository, proxy)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContractVersion != "domainry-runtime-module-build-closure-v1" ||
		!strings.HasPrefix(result.Version, "v0.0.0-source-") ||
		len(result.ZipSHA256) != 64 || len(result.GoModSHA256) != 64 ||
		len(result.InfoSHA256) != 64 || len(result.ListSHA256) != 64 || result.FileCount < 20 ||
		len(result.ClosureManifestSHA256) != 64 {
		t.Fatalf("result=%+v", result)
	}
	zipPath := filepath.Join(proxy, filepath.FromSlash(runtimeModulePath), "@v", result.Version+".zip")
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		lower := strings.ToLower(entry.Name)
		if strings.Contains(lower, "/scripts/") || strings.Contains(lower, "/evals/") ||
			strings.Contains(lower, "/docs/") || strings.Contains(lower, "golden") ||
			strings.HasSuffix(lower, "_test.go") {
			t.Fatalf("answer-bearing path entered module zip: %s", entry.Name)
		}
	}
	consumer := filepath.Join(t.TempDir(), "consumer")
	if err := os.MkdirAll(consumer, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/consumer\n\ngo 1.26.0\n\nrequire " + runtimeModulePath + " " + result.Version + "\n"
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package consumer
import (
	_ "github.com/domainry/domainry-connector-sdk"
	_ "github.com/domainry/domainry-runtime/pkg/runtimeext"
	_ "github.com/domainry/domainry-runtime/pkg/runtimehost"
)
`
	if err := os.WriteFile(filepath.Join(consumer, "consumer.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(),
		"GOWORK=off",
		"GOPROXY=file://"+filepath.ToSlash(proxy),
		"GONOPROXY=none",
		"GOSUMDB=off",
	)
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = consumer
	tidy.Env = environment
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("consumer tidy: %v\n%s", err, output)
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = consumer
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("consumer compile: %v\n%s", err, output)
	}
	for _, dependency := range []struct{ path, version string }{
		{"github.com/domainry/domainry-identity", identityModuleVersion},
		{"github.com/domainry/domainry-identity-sdk", "v0.1.0-dev2"},
	} {
		escapedPath, _ := module.EscapePath(dependency.path)
		escapedVersion, _ := module.EscapeVersion(dependency.version)
		for _, extension := range []string{".info", ".mod", ".zip"} {
			if _, err := os.Stat(filepath.Join(proxy, filepath.FromSlash(escapedPath), "@v", escapedVersion+extension)); err != nil {
				t.Fatalf("published dependency %s@%s%s: %v", dependency.path, dependency.version, extension, err)
			}
		}
	}
}

func TestForbiddenRuntimeModulePathRejectsAnswersAndFixtures(t *testing.T) {
	for _, path := range []string{
		"scripts/project/templates/inventory.go",
		"evals/hidden/assertions.json",
		"docs/todo.md",
		"runtime/example_test.go",
		"runtime/testdata/golden.json",
		"runtime/fixture.go",
	} {
		if !forbiddenRuntimeModulePath(path) {
			t.Fatalf("path was allowed: %s", path)
		}
	}
	for _, path := range []string{"go.mod", "go.sum", "pkg/runtimehost/runtime.go", "runtime/domain/model.go"} {
		if forbiddenRuntimeModulePath(path) {
			t.Fatalf("runtime closure path was rejected: %s", path)
		}
	}
}

func TestPublishResultIsJSONSerializable(t *testing.T) {
	if _, err := json.Marshal(publishResult{}); err != nil {
		t.Fatal(err)
	}
}
