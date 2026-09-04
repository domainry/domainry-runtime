package runtimehost_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestProjectMainCompilesUsingOnlyGeneratedCompositionAndRuntimehost(t *testing.T) {
	repositoryRoot := externalProjectRepositoryRoot(t)
	externalRoot := t.TempDir()
	writePinnedExternalProjectGoMod(t, repositoryRoot, externalRoot, "example.com/domainry-project")

	compositionDir := filepath.Join(externalRoot, "generated", "composition")
	if err := os.MkdirAll(compositionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	compositionSource := []byte(`package composition

import (
	agentmodule "github.com/domainry/domainry-agent/module"
	"github.com/domainry/domainry-connector-sdk"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationmodule "github.com/domainry/domainry-integration/module"
	monitoringmodule "github.com/domainry/domainry-monitoring/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	reportmodule "github.com/domainry/domainry-report/module"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/pkg/runtimehost"
)

func RuntimeOptions(runtimeVersion string) runtimehost.Options {
	return runtimehost.Options{
		Identity: runtimehost.BuildIdentity{
			RuntimeVersion: runtimeVersion,
			RuntimeextContractVersion: runtimeext.ContractVersion,
			RuntimeextContractSHA256: runtimeext.ContractSHA256,
			ConnectorContractVersion: connector.ContractVersion,
			ConnectorContractSHA256: connector.ContractSHA256,
		},
		IdentityFactory: identitymodule.NewFactory(identitymodule.OptionsFromEnvironment()),
		IntegrationFactory: integrationmodule.NewFactory(integrationmodule.OptionsFromEnvironment()),
		ReportFactory: reportmodule.NewFactory(),
		NotificationFactory: notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()),
		MonitoringFactory: monitoringmodule.NewFactory(monitoringmodule.OptionsFromEnvironment()),
		SchedulerFactory: schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()),
		DataExchangeFactory: dataexchangemodule.NewFactory(dataexchangemodule.Options{}),
		AgentFactory: agentmodule.NewFactory(agentmodule.OptionsFromEnvironment()),
	}
}
`)
	if err := os.WriteFile(filepath.Join(compositionDir, "extensions.gen.go"), compositionSource, 0o600); err != nil {
		t.Fatal(err)
	}
	writeExternalProjectMain(t, externalRoot, "example.com/domainry-project/generated/composition")
	compileExternalProject(t, repositoryRoot, externalRoot, "external Module project")
}

func TestProjectMainCompilesUsingSaaSFactoryWithoutIdentityModule(t *testing.T) {
	repositoryRoot := externalProjectRepositoryRoot(t)
	externalRoot := t.TempDir()
	writePinnedExternalProjectGoMod(t, repositoryRoot, externalRoot, "example.com/domainry-saas-project")

	compositionDir := filepath.Join(externalRoot, "generated", "composition")
	if err := os.MkdirAll(compositionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	compositionSource := []byte(`package composition

import (
	agentremote "github.com/domainry/domainry-agent/remote"
	"github.com/domainry/domainry-connector-sdk"
	dataexchangeremote "github.com/domainry/domainry-data-exchange/remote"
	identityremote "github.com/domainry/domainry-identity-sdk/remote"
	integrationremote "github.com/domainry/domainry-integration-sdk/remote"
	integrationmodule "github.com/domainry/domainry-integration/module"
	monitoringremote "github.com/domainry/domainry-monitoring-sdk/remote"
	notificationremote "github.com/domainry/domainry-notification-sdk/remote"
	notificationmodule "github.com/domainry/domainry-notification/module"
	reportmodule "github.com/domainry/domainry-report/module"
	schedulerremote "github.com/domainry/domainry-scheduler/remote"
	schedulerhttp "github.com/domainry/domainry-scheduler-sdk/saashost/httptransport"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/pkg/runtimehost"
)

func RuntimeOptions(runtimeVersion string) runtimehost.Options {
	return runtimehost.Options{
		Identity: runtimehost.BuildIdentity{
			RuntimeVersion: runtimeVersion,
			RuntimeextContractVersion: runtimeext.ContractVersion,
			RuntimeextContractSHA256: runtimeext.ContractSHA256,
			ConnectorContractVersion: connector.ContractVersion,
			ConnectorContractSHA256: connector.ContractSHA256,
		},
		IdentityFactory: identityremote.NewFactory(identityremote.ConfigFromEnvironment()),
		IntegrationFactory: integrationmodule.NewSaaSFactory(integrationremote.NewFactory(integrationremote.Options{})),
		ReportFactory: reportmodule.NewFactory(),
		NotificationFactory: notificationmodule.NewSaaSFactory(notificationremote.NewFactory(notificationremote.ConfigFromEnvironment())),
		MonitoringFactory: monitoringremote.NewFactory(monitoringremote.ConfigFromEnvironment()),
		SchedulerFactory: schedulerremote.NewHTTPFactory(schedulerhttp.ConfigFromEnvironment()),
		DataExchangeFactory: dataexchangeremote.NewFactory(nil),
		AgentFactory: agentremote.NewFactory(agentremote.OptionsFromEnvironment()),
	}
}
`)
	if err := os.WriteFile(filepath.Join(compositionDir, "extensions.gen.go"), compositionSource, 0o600); err != nil {
		t.Fatal(err)
	}
	writeExternalProjectMain(t, externalRoot, "example.com/domainry-saas-project/generated/composition")
	compileExternalProject(t, repositoryRoot, externalRoot, "external supported-SaaS project")
}

func externalProjectRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func writePinnedExternalProjectGoMod(t *testing.T, repositoryRoot, externalRoot, modulePath string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := modfile.Parse(filepath.Join(repositoryRoot, "go.mod"), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	generated := new(modfile.File)
	if err := generated.AddModuleStmt(modulePath); err != nil {
		t.Fatal(err)
	}
	if err := generated.AddGoStmt("1.26.0"); err != nil {
		t.Fatal(err)
	}
	if err := generated.AddRequire("github.com/domainry/domainry-runtime", "v0.0.0"); err != nil {
		t.Fatal(err)
	}
	for _, requirement := range pinned.Require {
		if requirement.Indirect || requirement.Mod.Path == "github.com/domainry/domainry-runtime" || !strings.HasPrefix(requirement.Mod.Path, "github.com/domainry/") {
			continue
		}
		if err := generated.AddRequire(requirement.Mod.Path, requirement.Mod.Version); err != nil {
			t.Fatal(err)
		}
		workspacePath := filepath.Join(filepath.Dir(repositoryRoot), strings.TrimPrefix(requirement.Mod.Path, "github.com/domainry/"))
		if info, statErr := os.Stat(workspacePath); statErr == nil && info.IsDir() {
			if err := generated.AddReplace(requirement.Mod.Path, "", workspacePath, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := generated.AddReplace("github.com/domainry/domainry-runtime", "", repositoryRoot, ""); err != nil {
		t.Fatal(err)
	}
	formatted, err := generated.Format()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalRoot, "go.mod"), formatted, 0o600); err != nil {
		t.Fatal(err)
	}
	// Seed the consumer with the Runtime-reviewed checksums so readonly mode
	// proves the pinned graph without asking the Go command to rewrite it.
	sums, err := os.ReadFile(filepath.Join(repositoryRoot, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalRoot, "go.sum"), sums, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeExternalProjectMain(t *testing.T, externalRoot, compositionImport string) {
	t.Helper()
	mainDir := filepath.Join(externalRoot, "cmd", "runtime")
	if err := os.MkdirAll(mainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mainSource := []byte("package main\n\nimport (\n\t\"" + compositionImport + "\"\n\t\"github.com/domainry/domainry-runtime/pkg/runtimehost\"\n\t\"os\"\n)\n\nvar runtimeVersion = \"dev\"\n\nfunc main() {\n\tos.Exit(runtimehost.RunCommand(os.Args[1:], os.Stdout, os.Stderr, composition.RuntimeOptions(runtimeVersion)))\n}\n")
	if err := os.WriteFile(filepath.Join(mainDir, "main.go"), mainSource, 0o600); err != nil {
		t.Fatal(err)
	}
}

func compileExternalProject(t *testing.T, repositoryRoot, externalRoot, label string) {
	t.Helper()
	runExternalGoCommand(t, externalRoot, label+" dependency normalization", "mod", "tidy")
	assertExternalProjectUsesPinnedDomainryModules(t, repositoryRoot, externalRoot, label)
	runExternalGoCommand(t, externalRoot, label+" compile", "test", "-mod=readonly", "./...")
}

func assertExternalProjectUsesPinnedDomainryModules(t *testing.T, repositoryRoot, externalRoot, label string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := modfile.Parse(filepath.Join(repositoryRoot, "go.mod"), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, requirement := range pinned.Require {
		if requirement.Mod.Path != "github.com/domainry/domainry-runtime" && strings.HasPrefix(requirement.Mod.Path, "github.com/domainry/") {
			want[requirement.Mod.Path] = requirement.Mod.Version
		}
	}
	output := runExternalGoCommand(t, externalRoot, label+" selected module graph", "list", "-mod=readonly", "-m", "-f={{.Path}}={{.Version}}", "all")
	selected := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		path, version, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			selected[path] = version
		}
	}
	for path, version := range want {
		if selected[path] != version {
			t.Errorf("%s selected %s@%s, Runtime pins %s", label, path, selected[path], version)
		}
	}
}

func runExternalGoCommand(t *testing.T, externalRoot, label string, arguments ...string) []byte {
	t.Helper()
	command := exec.Command("go", arguments...)
	command.Dir = externalRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", label, err, output)
	}
	return output
}
