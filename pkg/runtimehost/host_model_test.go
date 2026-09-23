package runtimehost

import (
	"errors"
	"strings"
	"testing"

	connector "github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func minimalHostOptions() Options {
	return Options{Identity: BuildIdentity{
		RuntimeVersion: "test", RuntimeextContractVersion: runtimeext.ContractVersion, ConnectorContractVersion: connector.ContractVersion,
	}}
}

func minimalProjectModelJSON() []byte {
	return []byte(`{
  "schema_version":"1",
  "project":{"key":"test","name":"Test","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{"item":{"name":"Item","fields":{"name":"text!"}}},
  "roles":{"administrator":{"name":"Administrator","permissions":["item.read;all"]}},
  "identity_profiles":{}
}`)
}

func modelBoundaryDependencies(readFile func(string) ([]byte, error)) serverRunDependencies {
	return serverRunDependencies{
		loadConfig: func() (config.Config, config.Snapshot, error) { return config.Config{}, config.Snapshot{}, nil },
		readFile:   readFile,
	}
}

func TestRuntimeHostRequiresOneStrictProjectModelBeforeFactoriesOrNetwork(t *testing.T) {
	options := minimalHostOptions()
	err := runWithDependencies(options, modelBoundaryDependencies(nil))
	if err == nil || !strings.Contains(err.Error(), "ModelFile is required") {
		t.Fatalf("missing model error=%v", err)
	}

	options.ModelFile = "backend/model.json"
	err = runWithDependencies(options, modelBoundaryDependencies(func(string) ([]byte, error) { return nil, errors.New("missing") }))
	if err == nil || !strings.Contains(err.Error(), "read project model backend/model.json") {
		t.Fatalf("read model error=%v", err)
	}

	err = runWithDependencies(options, modelBoundaryDependencies(func(string) ([]byte, error) {
		return []byte(`{"schema_version":"1","reports":{}}`), nil
	}))
	if err == nil || !strings.Contains(err.Error(), `unknown field "reports"`) {
		t.Fatalf("closed model error=%v", err)
	}

	err = runWithDependencies(options, modelBoundaryDependencies(func(string) ([]byte, error) { return minimalProjectModelJSON(), nil }))
	if err == nil || !strings.Contains(err.Error(), "configure Identity factory") {
		t.Fatalf("valid model did not reach factory composition: %v", err)
	}
}
