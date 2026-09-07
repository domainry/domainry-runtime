package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestSelectedDependencyProxyExcludesOlderTransitiveVersionAndBuildsOffline(t *testing.T) {
	const sdkPath = "github.com/domainry/example-sdk"
	const parentPath = "github.com/domainry/example-parent"
	staging, output := t.TempDir(), t.TempDir()
	var candidates []publishedDependencyModule
	for _, fixture := range []downloadedModule{
		downloadedModuleFixture(t, sdkPath, "v0.1.4"),
		downloadedModuleFixture(t, parentPath, "v0.1.0", sdkPath+" v0.1.3"),
		downloadedModuleFixture(t, sdkPath, "v0.1.3"),
	} {
		identity, err := copyDownloadedModule(staging, fixture)
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, identity)
	}
	selected, err := finalizePublishedDependencyModules(staging, candidates)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := copySelectedDependencyModules(staging, output, selected)
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	versions := make(map[string]string)
	for _, dependency := range sealed {
		versions[dependency.Path] = dependency.Version
		for _, suffix := range []string{".info", ".mod", ".zip"} {
			expected = append(expected, dependency.Path+"/@v/"+dependency.Version+suffix)
		}
		expected = append(expected, dependency.Path+"/@v/list")
		list, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(dependency.Path), "@v", "list"))
		if err != nil || string(list) != dependency.Version+"\n" || dependency.ListSHA256 != sha256Hex(list) {
			t.Fatalf("sealed list identity: %s %q %v", dependency.Path, list, err)
		}
	}
	if versions[sdkPath] != "v0.1.4" || len(versions) != 2 {
		t.Fatalf("sealed versions: %v", versions)
	}
	var actual []string
	if err := filepath.WalkDir(output, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			relative, err := filepath.Rel(output, path)
			if err != nil {
				return err
			}
			actual = append(actual, filepath.ToSlash(relative))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("release file closure differs: got %v want %v", actual, expected)
	}
	if _, err := os.Stat(filepath.Join(staging, filepath.FromSlash(sdkPath), "@v", "v0.1.3.mod")); err != nil {
		t.Fatalf("staging graph was mutated: %v", err)
	}

	consumer, moduleCache := t.TempDir(), t.TempDir()
	goMod, err := distributionGoModWithVersions([]byte("module example.com/consumer\n\ngo 1.26.0\n"), versions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	source := "package consumer\nimport (\n_ \"" + sdkPath + "\"\n_ \"" + parentPath + "\"\n)\n"
	if err := os.WriteFile(filepath.Join(consumer, "consumer.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"list", "-mod=mod", "-m", "all"}, {"test", "-mod=mod", "./..."}} {
		command := exec.Command("go", args...)
		command.Dir = consumer
		command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=-modcacherw", "GOMODCACHE="+moduleCache,
			"GOPROXY=file://"+filepath.ToSlash(output), "GOSUMDB=off", "GOPRIVATE=", "GONOPROXY=none", "GOVCS=*:off")
		result, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("offline consumer %v: %v\n%s", args, err, result)
		}
		if args[0] == "list" && (!strings.Contains(string(result), sdkPath+" v0.1.4") || strings.Contains(string(result), "v0.1.3")) {
			t.Fatalf("consumer selected unexpected graph:\n%s", result)
		}
	}
}

func TestSelectedDependencyProxyRejectsChangedSourceTuple(t *testing.T) {
	staging, output := t.TempDir(), t.TempDir()
	identity, err := copyDownloadedModule(staging, downloadedModuleFixture(t, "example.com/sdk", "v0.1.4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "example.com/sdk/@v/v0.1.4.mod"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := copySelectedDependencyModules(staging, output, []publishedDependencyModule{identity}); err == nil {
		t.Fatal("changed tuple accepted")
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected tuple reached release output: %v %v", entries, err)
	}
}
