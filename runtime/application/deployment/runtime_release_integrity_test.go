package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func TestRuntimeReleaseIntegrityReadinessChecksIndependentFacts(t *testing.T) {
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	handlerHash, err := deploymentmodel.RuntimeRegistrySHA256("domainry-handler-registry-v1", handlers.Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	connectorHash, err := deploymentmodel.RuntimeRegistrySHA256("domainry-connector-registry-v1", connectors.Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	identity := deploymentmodel.RuntimeReleaseIdentity{
		BuildMode: "packaged", DomainSDKContractVersion: "sdk-v1",
		HandlerRegistrySHA256: handlerHash, ConnectorRegistrySHA256: connectorHash,
	}
	currentSchema := "schema-1"
	service := NewRuntimeReleaseIntegrity(
		identity, RuntimeReleaseArtifactEvidence{Verified: true}, currentSchema,
		func(context.Context) (string, error) { return currentSchema, nil }, handlers, connectors,
	)
	for name, check := range map[string]func(context.Context) error{
		"build": service.BuildReadiness, "signature": service.SignatureReadiness,
		"schema": service.SchemaReadiness, "registry": service.RegistryReadiness,
	} {
		if err := check(t.Context()); err != nil {
			t.Fatalf("%s readiness: %v", name, err)
		}
	}

	service.artifact.BuildError = errors.New("binary checksum differs")
	if err := service.BuildReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseBuildIntegrity) {
		t.Fatalf("build drift error=%v", err)
	}
	service.artifact.BuildError = nil
	service.artifact.SignatureError = errors.New("signature differs")
	if err := service.SignatureReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseSignatureIntegrity) {
		t.Fatalf("signature drift error=%v", err)
	}
	service.artifact.SignatureError = nil
	currentSchema = "schema-2"
	if err := service.SchemaReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseSchemaIntegrity) || !strings.Contains(err.Error(), "schema-1") || !strings.Contains(err.Error(), "schema-2") {
		t.Fatalf("schema drift error=%v", err)
	}
	currentSchema = "schema-1"
	service.identity.HandlerRegistrySHA256 = strings.Repeat("a", 64)
	if err := service.RegistryReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseRegistryIntegrity) {
		t.Fatalf("registry drift error=%v", err)
	}
}

func TestRuntimeReleaseIntegrityDevelopmentAndGenericModes(t *testing.T) {
	projectDevelopment := NewRuntimeReleaseIntegrity(
		deploymentmodel.RuntimeReleaseIdentity{BuildMode: "development", DomainSDKContractVersion: "sdk-v1"},
		RuntimeReleaseArtifactEvidence{}, "schema", func(context.Context) (string, error) { return "schema", nil },
		nil, nil,
	)
	if err := projectDevelopment.BuildReadiness(t.Context()); err != nil {
		t.Fatalf("development build readiness=%v", err)
	}
	if err := projectDevelopment.SignatureReadiness(t.Context()); err != nil {
		t.Fatalf("development signature readiness=%v", err)
	}
	if err := projectDevelopment.RegistryReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseRegistryIntegrity) {
		t.Fatalf("project development Registry readiness=%v", err)
	}

	generic := NewRuntimeReleaseIntegrity(
		deploymentmodel.RuntimeReleaseIdentity{BuildMode: "development"},
		RuntimeReleaseArtifactEvidence{}, "", nil, nil, nil,
	)
	for _, check := range []func(context.Context) error{
		generic.BuildReadiness, generic.SignatureReadiness, generic.SchemaReadiness, generic.RegistryReadiness,
	} {
		if err := check(t.Context()); err != nil {
			t.Fatalf("generic readiness=%v", err)
		}
	}
}
