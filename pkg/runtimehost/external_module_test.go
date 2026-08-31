package runtimehost_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProjectMainCompilesUsingOnlyGeneratedCompositionAndRuntimehost(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current file")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	identitySDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-identity-sdk")
	identityModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-identity")
	auditSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-audit-sdk")
	auditModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-audit")
	notificationSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-notification-sdk")
	notificationModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-notification")
	monitoringSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-monitoring-sdk")
	monitoringModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-monitoring")
	schedulerSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-scheduler-sdk")
	schedulerModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-scheduler")
	dataExchangeSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-data-exchange-sdk")
	dataExchangeModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-data-exchange")
	agentSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-agent-sdk")
	agentModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-agent")
	reportSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-report-sdk")
	reportModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-report")
	metadataSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-metadata-sdk")
	metadataModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-metadata")
	integrationSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-integration-sdk")
	integrationModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-integration")
	lifecycleSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-lifecycle-sdk")
	lifecycleModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-lifecycle")
	partySDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-party-sdk")
	partyModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-party")
	foundationRoot := siblingModuleRoot(t, repositoryRoot, "domainry-foundation")
	ormRoot := siblingModuleRoot(t, repositoryRoot, "domainry-orm")
	externalRoot := t.TempDir()
	goMod := []byte("module example.com/domainry-project\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/domainry/domainry-runtime v0.0.0\n\tgithub.com/domainry/domainry-identity v0.0.0\n\tgithub.com/domainry/domainry-notification v0.0.0\n\tgithub.com/domainry/domainry-party v0.0.0\n\tgithub.com/domainry/domainry-monitoring v0.0.0\n\tgithub.com/domainry/domainry-scheduler v0.0.0\n)\n\nreplace github.com/domainry/domainry-runtime => " + repositoryRoot + "\nreplace github.com/domainry/domainry-foundation => " + foundationRoot + "\nreplace github.com/domainry/domainry-orm => " + ormRoot + "\nreplace github.com/domainry/domainry-audit-sdk => " + auditSDKRoot + "\nreplace github.com/domainry/domainry-audit => " + auditModuleRoot + "\nreplace github.com/domainry/domainry-identity-sdk => " + identitySDKRoot + "\nreplace github.com/domainry/domainry-identity => " + identityModuleRoot + "\nreplace github.com/domainry/domainry-notification-sdk => " + notificationSDKRoot + "\nreplace github.com/domainry/domainry-notification => " + notificationModuleRoot + "\nreplace github.com/domainry/domainry-party-sdk => " + partySDKRoot + "\nreplace github.com/domainry/domainry-party => " + partyModuleRoot + "\nreplace github.com/domainry/domainry-monitoring-sdk => " + monitoringSDKRoot + "\nreplace github.com/domainry/domainry-monitoring => " + monitoringModuleRoot + "\nreplace github.com/domainry/domainry-scheduler-sdk => " + schedulerSDKRoot + "\nreplace github.com/domainry/domainry-scheduler => " + schedulerModuleRoot + "\n")
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-data-exchange-sdk => "+dataExchangeSDKRoot+"\nreplace github.com/domainry/domainry-data-exchange => "+dataExchangeModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-agent-sdk => "+agentSDKRoot+"\nreplace github.com/domainry/domainry-agent => "+agentModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-report-sdk => "+reportSDKRoot+"\nreplace github.com/domainry/domainry-report => "+reportModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-metadata-sdk => "+metadataSDKRoot+"\nreplace github.com/domainry/domainry-metadata => "+metadataModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-integration-sdk => "+integrationSDKRoot+"\nreplace github.com/domainry/domainry-integration => "+integrationModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-lifecycle-sdk => "+lifecycleSDKRoot+"\nreplace github.com/domainry/domainry-lifecycle => "+lifecycleModuleRoot+"\n")...)
	if err := os.WriteFile(filepath.Join(externalRoot, "go.mod"), goMod, 0o600); err != nil {
		t.Fatal(err)
	}
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
	notificationmodule "github.com/domainry/domainry-notification/module"
	partymodule "github.com/domainry/domainry-party/module"
	monitoringmodule "github.com/domainry/domainry-monitoring/module"
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
		NotificationFactory: notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()),
		PartyFactory: partymodule.NewFactory(partymodule.OptionsFromEnvironment()),
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
	compileExternalProject(t, externalRoot, "external Module project")
}

func TestProjectMainCompilesUsingSaaSFactoryWithoutIdentityModule(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current file")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	identitySDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-identity-sdk")
	auditSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-audit-sdk")
	auditModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-audit")
	notificationSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-notification-sdk")
	partySDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-party-sdk")
	monitoringSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-monitoring-sdk")
	schedulerSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-scheduler-sdk")
	schedulerModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-scheduler")
	dataExchangeSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-data-exchange-sdk")
	dataExchangeModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-data-exchange")
	agentSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-agent-sdk")
	agentModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-agent")
	reportSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-report-sdk")
	reportModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-report")
	metadataSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-metadata-sdk")
	metadataModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-metadata")
	integrationSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-integration-sdk")
	integrationModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-integration")
	lifecycleSDKRoot := siblingModuleRoot(t, repositoryRoot, "domainry-lifecycle-sdk")
	lifecycleModuleRoot := siblingModuleRoot(t, repositoryRoot, "domainry-lifecycle")
	foundationRoot := siblingModuleRoot(t, repositoryRoot, "domainry-foundation")
	ormRoot := siblingModuleRoot(t, repositoryRoot, "domainry-orm")
	externalRoot := t.TempDir()
	goMod := []byte("module example.com/domainry-saas-project\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/domainry/domainry-runtime v0.0.0\n\tgithub.com/domainry/domainry-identity-sdk v0.0.0\n\tgithub.com/domainry/domainry-notification-sdk v0.0.0\n\tgithub.com/domainry/domainry-party-sdk v0.0.0\n\tgithub.com/domainry/domainry-monitoring-sdk v0.0.0\n\tgithub.com/domainry/domainry-scheduler v0.0.0\n)\n\nreplace github.com/domainry/domainry-runtime => " + repositoryRoot + "\nreplace github.com/domainry/domainry-foundation => " + foundationRoot + "\nreplace github.com/domainry/domainry-orm => " + ormRoot + "\nreplace github.com/domainry/domainry-audit-sdk => " + auditSDKRoot + "\nreplace github.com/domainry/domainry-audit => " + auditModuleRoot + "\nreplace github.com/domainry/domainry-identity-sdk => " + identitySDKRoot + "\nreplace github.com/domainry/domainry-notification-sdk => " + notificationSDKRoot + "\nreplace github.com/domainry/domainry-party-sdk => " + partySDKRoot + "\nreplace github.com/domainry/domainry-monitoring-sdk => " + monitoringSDKRoot + "\nreplace github.com/domainry/domainry-scheduler-sdk => " + schedulerSDKRoot + "\nreplace github.com/domainry/domainry-scheduler => " + schedulerModuleRoot + "\n")
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-data-exchange-sdk => "+dataExchangeSDKRoot+"\nreplace github.com/domainry/domainry-data-exchange => "+dataExchangeModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-agent-sdk => "+agentSDKRoot+"\nreplace github.com/domainry/domainry-agent => "+agentModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-report-sdk => "+reportSDKRoot+"\nreplace github.com/domainry/domainry-report => "+reportModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-metadata-sdk => "+metadataSDKRoot+"\nreplace github.com/domainry/domainry-metadata => "+metadataModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-integration-sdk => "+integrationSDKRoot+"\nreplace github.com/domainry/domainry-integration => "+integrationModuleRoot+"\n")...)
	goMod = append(goMod, []byte("replace github.com/domainry/domainry-lifecycle-sdk => "+lifecycleSDKRoot+"\nreplace github.com/domainry/domainry-lifecycle => "+lifecycleModuleRoot+"\n")...)
	if err := os.WriteFile(filepath.Join(externalRoot, "go.mod"), goMod, 0o600); err != nil {
		t.Fatal(err)
	}
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
	notificationremote "github.com/domainry/domainry-notification-sdk/remote"
	partyremote "github.com/domainry/domainry-party-sdk/remote"
	monitoringremote "github.com/domainry/domainry-monitoring-sdk/remote"
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
		NotificationFactory: notificationremote.NewFactory(notificationremote.ConfigFromEnvironment()),
		PartyFactory: partyremote.NewFactory(partyremote.Config{}),
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
	compileExternalProject(t, externalRoot, "external SaaS project")
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

func siblingModuleRoot(t *testing.T, repositoryRoot, name string) string {
	t.Helper()
	candidate := filepath.Clean(filepath.Join(repositoryRoot, "..", name))
	if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
		return candidate
	}
	data, err := os.ReadFile(filepath.Join(repositoryRoot, ".git"))
	if err != nil {
		t.Fatalf("resolve canonical repository for %s: %v", name, err)
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	marker := string(filepath.Separator) + ".git" + string(filepath.Separator)
	index := strings.Index(gitdir, marker)
	if index < 0 {
		t.Fatalf("resolve canonical git directory from %q", gitdir)
	}
	canonical := gitdir[:index]
	candidate = filepath.Join(filepath.Dir(canonical), name)
	if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err != nil {
		t.Fatalf("resolve sibling module %s: %v", name, err)
	}
	return candidate
}

func compileExternalProject(t *testing.T, externalRoot, label string) {
	t.Helper()
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = externalRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s cannot compile runtimehost composition: %v\n%s", label, err, output)
	}
}
