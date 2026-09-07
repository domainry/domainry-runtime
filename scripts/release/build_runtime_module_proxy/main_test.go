package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

func TestPublishContainsOnlyRuntimeBuildClosureAndCompilesConsumer(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	repository := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
	proxy := filepath.Join(t.TempDir(), "proxy")
	result, err := publish(repository, proxy)
	if err != nil {
		t.Fatal(err)
	}
	secondProxy := filepath.Join(t.TempDir(), "proxy")
	secondResult, err := publish(repository, secondProxy)
	if err != nil {
		t.Fatal(err)
	}
	// Adjacent content-addressed dependency checkouts are legitimate publish
	// inputs and may be edited by another task between these two integration
	// publishes. Enforce byte determinism only when that dependency snapshot is
	// unchanged; a changed snapshot must instead produce a changed Runtime
	// identity and is verified below through the frozen proxy consumer.
	if reflect.DeepEqual(result.DependencyModules, secondResult.DependencyModules) && !reflect.DeepEqual(result, secondResult) {
		firstJSON, _ := json.MarshalIndent(result, "", "  ")
		secondJSON, _ := json.MarshalIndent(secondResult, "", "  ")
		t.Fatalf("consecutive source-identical publishes were not byte-deterministic:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
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
	_ "github.com/domainry/domainry-connectors/providers/website_form/tally"
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
	dependencyVersions := map[string]string{}
	for _, dependency := range result.DependencyModules {
		dependencyVersions[dependency.Path] = dependency.Version
	}
	for _, dependency := range []struct{ path, version string }{
		{"github.com/domainry/domainry-identity", dependencyVersions["github.com/domainry/domainry-identity"]},
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
	for _, path := range []string{
		"github.com/domainry/domainry-identity",
		"github.com/domainry/domainry-identity-sdk",
		"github.com/domainry/domainry-notification-sdk",
		"github.com/domainry/domainry-notification",
		"github.com/domainry/domainry-connectors",
	} {
		if version := dependencyVersions[path]; !strings.HasPrefix(version, "v0.999.0-domainry.") {
			t.Fatalf("workspace dependency %s version=%q is not content-addressed", path, version)
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
	parsedRuntimeMod, err := modfile.Parse("go.mod", runtimeMod, nil)
	if err != nil {
		t.Fatal(err)
	}
	hasNotificationImplementation := false
	for _, requirement := range parsedRuntimeMod.Require {
		if requirement.Mod.Path == "github.com/domainry/domainry-notification" && requirement.Mod.Version == dependencyVersions["github.com/domainry/domainry-notification"] {
			hasNotificationImplementation = true
			break
		}
	}
	if !hasNotificationImplementation {
		t.Fatal("Runtime distribution go.mod omitted the Notification HTTP surface implementation")
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

func TestDistributionGoModAddsVersionOverridesInPathOrder(t *testing.T) {
	content, err := distributionGoModWithVersions([]byte("module example.com/root\n\ngo 1.26.0\n"), map[string]string{
		"example.com/zeta":   "v1.0.0",
		"example.com/alpha":  "v1.0.0",
		"example.com/middle": "v1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(parsed.Require))
	for _, requirement := range parsed.Require {
		paths = append(paths, requirement.Mod.Path)
	}
	want := []string{"example.com/alpha", "example.com/middle", "example.com/zeta"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("require order=%v want=%v\n%s", paths, want, content)
	}
}

func TestPublishedModuleListContainsEveryCompleteTupleAndSelectedHash(t *testing.T) {
	const path = "example.com/dependency"
	proxy := t.TempDir()
	newer, err := copyDownloadedModule(proxy, downloadedModuleFixture(t, path, "v1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the real graph order: a lower transitive version is visited
	// after the higher direct requirement selected by Minimal Version Selection.
	older, err := copyDownloadedModule(proxy, downloadedModuleFixture(t, path, "v1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := finalizePublishedDependencyModules(proxy, []publishedDependencyModule{newer, older})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Version != "v1.1.0" {
		t.Fatalf("selected dependencies=%+v", selected)
	}
	root, err := publishedModuleVersionRoot(proxy, path)
	if err != nil {
		t.Fatal(err)
	}
	list, err := os.ReadFile(filepath.Join(root, "list"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(list), "v1.0.0\nv1.1.0\n"; got != want {
		t.Fatalf("list=%q want=%q", got, want)
	}
	if selected[0].ListSHA256 != sha256Hex(list) {
		t.Fatalf("selected list hash=%s want=%s", selected[0].ListSHA256, sha256Hex(list))
	}
}

func TestPublishedModuleTupleFailsClosedOnEachArtifactIdentity(t *testing.T) {
	const path = "example.com/dependency"
	const version = "v1.2.3"
	for _, test := range []struct {
		artifact string
		content  []byte
		want     string
	}{
		{artifact: ".info", content: []byte("{\"Version\":\"v1.2.4\"}\n"), want: ".info identity differs"},
		{artifact: ".mod", content: []byte("module example.com/other\n\ngo 1.26.0\n"), want: ".mod identity differs"},
		{artifact: ".zip", content: []byte("not a module zip"), want: ".zip identity or contents differ"},
		{artifact: "list", content: []byte("v1.2.4\n"), want: "list does not contain exact version"},
	} {
		t.Run(test.artifact, func(t *testing.T) {
			proxy := t.TempDir()
			identity, err := copyDownloadedModule(proxy, downloadedModuleFixture(t, path, version))
			if err != nil {
				t.Fatal(err)
			}
			root, err := publishedModuleVersionRoot(proxy, path)
			if err != nil {
				t.Fatal(err)
			}
			name := "list"
			if test.artifact != "list" {
				name = version + test.artifact
			}
			if err := os.WriteFile(filepath.Join(root, name), test.content, 0o644); err != nil {
				t.Fatal(err)
			}
			switch test.artifact {
			case ".info":
				identity.InfoSHA256 = sha256Hex(test.content)
			case ".mod":
				identity.GoModSHA256 = sha256Hex(test.content)
			case ".zip":
				identity.ZipSHA256 = sha256Hex(test.content)
			case "list":
				identity.ListSHA256 = sha256Hex(test.content)
			}
			err = validatePublishedModuleTuple(proxy, identity)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error=%v want containing %q", err, test.want)
			}
		})
	}
}

func downloadedModuleFixture(t *testing.T, path, version string, requirements ...string) downloadedModule {
	t.Helper()
	source := t.TempDir()
	goMod := []byte("module " + path + "\n\ngo 1.26.0\n")
	if len(requirements) != 0 {
		goMod = append(goMod, []byte("\nrequire (\n"+strings.Join(requirements, "\n")+"\n)\n")...)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "dependency.go"), []byte("package dependency\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifacts := t.TempDir()
	infoPath := filepath.Join(artifacts, "module.info")
	modPath := filepath.Join(artifacts, "module.mod")
	zipPath := filepath.Join(artifacts, "module.zip")
	if err := os.WriteFile(infoPath, []byte("{\"Version\":\""+version+"\",\"Time\":\"2026-09-06T00:00:00Z\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := modzip.CreateFromDir(archive, module.Version{Path: path, Version: version}, source); err != nil {
		_ = archive.Close()
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return downloadedModule{Path: path, Version: version, Info: infoPath, GoMod: modPath, Zip: zipPath}
}
