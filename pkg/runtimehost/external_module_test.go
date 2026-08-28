package runtimehost_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProjectMainCompilesUsingOnlyGeneratedCompositionAndRuntimehost(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current file")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	identitySDKRoot := filepath.Clean(filepath.Join(repositoryRoot, "..", "domainry-identity-sdk"))
	identityModuleRoot := filepath.Clean(filepath.Join(repositoryRoot, "..", "domainry-identity"))
	notificationSDKRoot := filepath.Clean(filepath.Join(repositoryRoot, "..", "domainry-notification-sdk"))
	notificationModuleRoot := filepath.Clean(filepath.Join(repositoryRoot, "..", "domainry-notification"))
	externalRoot := t.TempDir()
	goMod := []byte("module example.com/domainry-project\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/domainry/domainry-runtime v0.0.0\n\tgithub.com/domainry/domainry-identity v0.0.0\n\tgithub.com/domainry/domainry-notification v0.0.0\n)\n\nreplace github.com/domainry/domainry-runtime => " + repositoryRoot + "\nreplace github.com/domainry/domainry-identity-sdk => " + identitySDKRoot + "\nreplace github.com/domainry/domainry-identity => " + identityModuleRoot + "\nreplace github.com/domainry/domainry-notification-sdk => " + notificationSDKRoot + "\nreplace github.com/domainry/domainry-notification => " + notificationModuleRoot + "\n")
	if err := os.WriteFile(filepath.Join(externalRoot, "go.mod"), goMod, 0o600); err != nil {
		t.Fatal(err)
	}
	compositionDir := filepath.Join(externalRoot, "generated", "composition")
	if err := os.MkdirAll(compositionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	compositionSource := []byte(`package composition

import (
	"github.com/domainry/domainry-connector-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
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
	identitySDKRoot := filepath.Clean(filepath.Join(repositoryRoot, "..", "domainry-identity-sdk"))
	notificationSDKRoot := filepath.Clean(filepath.Join(repositoryRoot, "..", "domainry-notification-sdk"))
	externalRoot := t.TempDir()
	goMod := []byte("module example.com/domainry-saas-project\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/domainry/domainry-runtime v0.0.0\n\tgithub.com/domainry/domainry-identity-sdk v0.0.0\n\tgithub.com/domainry/domainry-notification-sdk v0.0.0\n)\n\nreplace github.com/domainry/domainry-runtime => " + repositoryRoot + "\nreplace github.com/domainry/domainry-identity-sdk => " + identitySDKRoot + "\nreplace github.com/domainry/domainry-notification-sdk => " + notificationSDKRoot + "\n")
	if err := os.WriteFile(filepath.Join(externalRoot, "go.mod"), goMod, 0o600); err != nil {
		t.Fatal(err)
	}
	compositionDir := filepath.Join(externalRoot, "generated", "composition")
	if err := os.MkdirAll(compositionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	compositionSource := []byte(`package composition

import (
	"github.com/domainry/domainry-connector-sdk"
	identityremote "github.com/domainry/domainry-identity-sdk/remote"
	notificationremote "github.com/domainry/domainry-notification-sdk/remote"
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

func compileExternalProject(t *testing.T, externalRoot, label string) {
	t.Helper()
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = externalRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s cannot compile runtimehost composition: %v\n%s", label, err, output)
	}
}
