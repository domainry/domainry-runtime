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

	"golang.org/x/mod/modfile"
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
	moduleCache := filepath.Join(t.TempDir(), "gomodcache")
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
		"GOMODCACHE="+moduleCache,
		"GOFLAGS=-modcacherw",
		"GOPROXY=file://"+filepath.ToSlash(proxy)+",https://proxy.golang.org,direct",
		// A developer machine commonly marks github.com/domainry/* private.
		// The frozen-proxy consumer must not inherit that setting and bypass the
		// artifacts under test by consulting a VCS checkout instead.
		"GOPRIVATE=",
		"GONOPROXY=off",
		"GONOSUMDB=github.com/domainry/*",
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
	if matches, err := filepath.Glob(filepath.Join(moduleCache, "github.com", "domainry", "domainry-notification@*")); err != nil {
		t.Fatal(err)
	} else if len(matches) != 0 {
		t.Fatalf("SDK-only Runtime consumer downloaded optional Notification implementation: %v", matches)
	}
	dependencyVersions := map[string]string{}
	for _, dependency := range result.DependencyModules {
		dependencyVersions[dependency.Path] = dependency.Version
	}
	for _, dependency := range []struct{ path, version string }{
		{"github.com/domainry/domainry-identity", identityModuleVersion},
		{"github.com/domainry/domainry-identity-sdk", dependencyVersions["github.com/domainry/domainry-identity-sdk"]},
	} {
		if dependency.version == "" {
			t.Fatalf("published dependency %s has no selected version", dependency.path)
		}
		escapedPath, _ := module.EscapePath(dependency.path)
		escapedVersion, _ := module.EscapeVersion(dependency.version)
		for _, extension := range []string{".info", ".mod", ".zip"} {
			if _, err := os.Stat(filepath.Join(proxy, filepath.FromSlash(escapedPath), "@v", escapedVersion+extension)); err != nil {
				t.Fatalf("published dependency %s@%s%s: %v", dependency.path, dependency.version, extension, err)
			}
		}
	}
	for path, wantVersion := range map[string]string{
		"github.com/domainry/domainry-identity-sdk":     "v0.1.0-dev9",
		"github.com/domainry/domainry-notification-sdk": "v0.1.0-dev.5",
		"github.com/domainry/domainry-notification":     "v0.1.0-dev.12",
	} {
		if version := dependencyVersions[path]; version != wantVersion {
			t.Fatalf("released dependency %s version=%q, want %q", path, version, wantVersion)
		}
	}
	runtimeMod, err := os.ReadFile(filepath.Join(proxy, filepath.FromSlash(runtimeModulePath), "@v", result.Version+".mod"))
	if err != nil {
		t.Fatal(err)
	}
	for path, version := range dependencyVersions {
		if path == "github.com/domainry/domainry-notification-sdk" && !strings.Contains(string(runtimeMod), path+" "+version) {
			t.Fatalf("Runtime distribution go.mod does not reference %s@%s", path, version)
		}
	}
	if strings.Contains(string(runtimeMod), "github.com/domainry/domainry-notification ") {
		t.Fatal("Runtime distribution go.mod retained the optional Notification implementation module")
	}
	moduleFiles, err := filepath.Glob(filepath.Join(proxy, "github.com", "domainry", "*", "@v", "*.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(moduleFiles) == 0 {
		t.Fatal("frozen proxy did not contain any published go.mod files")
	}
	for _, moduleFile := range moduleFiles {
		contents, err := os.ReadFile(moduleFile)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := modfile.Parse(moduleFile, contents, nil)
		if err != nil {
			t.Fatalf("parse published go.mod %s: %v", moduleFile, err)
		}
		if len(parsed.Replace) != 0 {
			t.Fatalf("published go.mod retained development replace directives: %s", moduleFile)
		}
	}
	notificationPath, _ := module.EscapePath("github.com/domainry/domainry-notification")
	notificationVersion, _ := module.EscapeVersion(dependencyVersions["github.com/domainry/domainry-notification"])
	notificationMod, err := os.ReadFile(filepath.Join(proxy, filepath.FromSlash(notificationPath), "@v", notificationVersion+".mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notificationMod), "github.com/domainry/domainry-notification-sdk "+dependencyVersions["github.com/domainry/domainry-notification-sdk"]) {
		t.Fatal("Notification distribution go.mod does not reference the selected SDK tag")
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

func TestContentVersionIncludesFinalDistributionClosure(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	files := []string{"go.mod", "go.sum"}
	for _, root := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/domainry/domainry-runtime\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(first, "go.sum"), []byte("dependency v1 h1:first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "go.sum"), []byte("dependency v1 h1:second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if contentVersion(first, files) == contentVersion(second, files) {
		t.Fatal("final dependency closure did not affect immutable module version")
	}
}
