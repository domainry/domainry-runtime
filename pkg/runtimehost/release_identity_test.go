package runtimehost

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

type releaseIdentityTestHandler struct{ revision string }

type releaseIdentityWorkspaceBootstrap struct {
	descriptor runtimeext.WorkspaceBootstrapDescriptor
}

type releaseIdentityAssigneeResolver struct {
	descriptor runtimeext.AssigneeResolverDescriptor
}

func (r releaseIdentityAssigneeResolver) Descriptor() runtimeext.AssigneeResolverDescriptor {
	return r.descriptor
}

func (releaseIdentityAssigneeResolver) Resolve(context.Context, runtimeext.AssigneeResolverCapabilities, runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
	return nil, nil
}

func releaseIdentityResolver(revision string) releaseIdentityAssigneeResolver {
	descriptor := runtimeext.AssigneeResolverDescriptor{ResolverKey: "finance.approver", ResolverRevision: revision, MaxReadOperations: 1, MaxCandidates: 1, TimeoutMilliseconds: 100}
	descriptor.ConfigContractSHA256 = descriptor.ComputedConfigContractSHA256()
	return releaseIdentityAssigneeResolver{descriptor: descriptor}
}

func (p releaseIdentityWorkspaceBootstrap) Descriptor() runtimeext.WorkspaceBootstrapDescriptor {
	return p.descriptor
}

func (releaseIdentityWorkspaceBootstrap) BuildWorkspaceBootstrap(context.Context, runtimeext.WorkspaceBootstrapContext, map[string]any) ([]runtimeext.WorkspaceBootstrapRecord, error) {
	return nil, nil
}

func releaseIdentityBootstrapParticipant(revision string) releaseIdentityWorkspaceBootstrap {
	descriptor := runtimeext.WorkspaceBootstrapDescriptor{
		Key: "workspace.bootstrap", InputType: "generated/bootstrap.WorkspaceInput", ParticipantRevision: revision,
		Records: []runtimeext.WorkspaceBootstrapRecordCapability{{Key: "settings", ObjectKey: "settings", Fields: []string{"name"}}},
	}
	descriptor.InputContractSHA256 = descriptor.ComputedInputContractSHA256()
	return releaseIdentityWorkspaceBootstrap{descriptor: descriptor}
}

func (h releaseIdentityTestHandler) Descriptor() runtimeext.HandlerDescriptor {
	return runtimeext.HandlerDescriptor{
		ActionKey: "booking.reserve", InputType: "generated/booking.ReserveInput", OutputType: "generated/booking.ReserveOutput",
		HandlerRevision: h.revision,
	}
}

func (releaseIdentityTestHandler) Invoke(context.Context, runtimeext.ActionExecution, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func TestRuntimeReleaseIdentityPublishesDeterministicComposition(t *testing.T) {
	handlers := runtimeext.NewProjectExtensionRegistry()
	if err := handlers.RegisterBusinessHandler(releaseIdentityTestHandler{revision: "revision-1"}); err != nil {
		t.Fatal(err)
	}
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	first, err := runtimeReleaseIdentity(validOptions().Identity, handlers, connectors)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtimeReleaseIdentity(validOptions().Identity, handlers, connectors)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.ContractVersion != RuntimeReleaseIdentityVersion || first.BuildMode != "development" || !lowerSHA256(first.ProjectExtensionRegistrySHA256) || !lowerSHA256(first.ConnectorRegistrySHA256) || !lowerSHA256(first.CombinationSHA256) {
		t.Fatalf("release identity=%+v second=%+v", first, second)
	}

	changedHandlers := runtimeext.NewProjectExtensionRegistry()
	if err := changedHandlers.RegisterBusinessHandler(releaseIdentityTestHandler{revision: "revision-2"}); err != nil {
		t.Fatal(err)
	}
	changedHandlers.Freeze()
	changed, err := runtimeReleaseIdentity(validOptions().Identity, changedHandlers, connectors)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ProjectExtensionRegistrySHA256 == first.ProjectExtensionRegistrySHA256 || changed.CombinationSHA256 == first.CombinationSHA256 {
		t.Fatalf("registry drift did not change combination: first=%+v changed=%+v", first, changed)
	}

	withBootstrap := runtimeext.NewProjectExtensionRegistry()
	if err := withBootstrap.RegisterProjectExtensions(runtimeext.ProjectExtensions{
		BusinessHandlers:              []runtimeext.BusinessHandler{releaseIdentityTestHandler{revision: "revision-1"}},
		WorkspaceBootstrapParticipant: releaseIdentityBootstrapParticipant("bootstrap-revision-1"),
	}); err != nil {
		t.Fatal(err)
	}
	withBootstrap.Freeze()
	bootstrapIdentity, err := runtimeReleaseIdentity(validOptions().Identity, withBootstrap, connectors)
	if err != nil {
		t.Fatal(err)
	}
	changedBootstrap := runtimeext.NewProjectExtensionRegistry()
	if err := changedBootstrap.RegisterProjectExtensions(runtimeext.ProjectExtensions{
		BusinessHandlers:              []runtimeext.BusinessHandler{releaseIdentityTestHandler{revision: "revision-1"}},
		WorkspaceBootstrapParticipant: releaseIdentityBootstrapParticipant("bootstrap-revision-2"),
	}); err != nil {
		t.Fatal(err)
	}
	changedBootstrap.Freeze()
	changedBootstrapIdentity, err := runtimeReleaseIdentity(validOptions().Identity, changedBootstrap, connectors)
	if err != nil {
		t.Fatal(err)
	}
	if changedBootstrapIdentity.ProjectExtensionRegistrySHA256 == bootstrapIdentity.ProjectExtensionRegistrySHA256 || changedBootstrapIdentity.CombinationSHA256 == bootstrapIdentity.CombinationSHA256 {
		t.Fatalf("Workspace Bootstrap drift did not change release identity: first=%+v changed=%+v", bootstrapIdentity, changedBootstrapIdentity)
	}
	withResolver := runtimeext.NewProjectExtensionRegistry()
	if err := withResolver.RegisterAssigneeResolver(releaseIdentityResolver("resolver-revision-1")); err != nil {
		t.Fatal(err)
	}
	withResolver.Freeze()
	resolverIdentity, err := runtimeReleaseIdentity(validOptions().Identity, withResolver, connectors)
	if err != nil {
		t.Fatal(err)
	}
	changedResolver := runtimeext.NewProjectExtensionRegistry()
	if err := changedResolver.RegisterAssigneeResolver(releaseIdentityResolver("resolver-revision-2")); err != nil {
		t.Fatal(err)
	}
	changedResolver.Freeze()
	changedResolverIdentity, err := runtimeReleaseIdentity(validOptions().Identity, changedResolver, connectors)
	if err != nil {
		t.Fatal(err)
	}
	if changedResolverIdentity.ProjectExtensionRegistrySHA256 == resolverIdentity.ProjectExtensionRegistrySHA256 || changedResolverIdentity.CombinationSHA256 == resolverIdentity.CombinationSHA256 {
		t.Fatalf("Assignee Resolver drift did not change release identity: first=%+v changed=%+v", resolverIdentity, changedResolverIdentity)
	}
}

func TestRuntimeReleaseIdentityValidatesPackagedLinkerFacts(t *testing.T) {
	restore := setRuntimeReleaseLinkerFactsForTest()
	defer restore()
	handlers := runtimeext.NewProjectExtensionRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	identity, err := runtimeReleaseIdentity(validOptions().Identity, handlers, connectors)
	if err != nil {
		t.Fatal(err)
	}
	if identity.BuildMode != "packaged" || identity.ProjectModule != "example.com/project" || identity.GeneratedSDKSHA256 != validOptions().Identity.DomainSDK.ArtifactSHA256 || identity.HandlerCatalogSHA256 != strings.Repeat("e", 64) || identity.SigningKeyID != "project-release" {
		t.Fatalf("packaged identity=%+v", identity)
	}
	builtProjectSourceSHA256 = ""
	if _, err := runtimeReleaseIdentity(validOptions().Identity, handlers, connectors); !errors.Is(err, ErrRuntimeReleaseIdentity) {
		t.Fatalf("partial linker identity error=%v", err)
	}
}

func TestRuntimeReleaseIdentityLinkerInjection(t *testing.T) {
	if os.Getenv("DOMAINRY_RELEASE_LINKER_CHILD") == "1" {
		if builtProjectModule != "example.com/linked-project" || builtProjectSourceSHA256 != strings.Repeat("9", 64) || builtSigningKeyID != "linked-release" {
			t.Fatalf("linker values were not injected: module=%q source=%q key=%q", builtProjectModule, builtProjectSourceSHA256, builtSigningKeyID)
		}
		return
	}
	const packagePath = "github.com/domainry/domainry-runtime/pkg/runtimehost."
	flags := strings.Join([]string{
		"-X", packagePath + "builtProjectModule=example.com/linked-project",
		"-X", packagePath + "builtProjectSourceSHA256=" + strings.Repeat("9", 64),
		"-X", packagePath + "builtSigningKeyID=linked-release",
	}, " ")
	command := exec.Command("go", "test", "-run", "^TestRuntimeReleaseIdentityLinkerInjection$", "-count=1", "-ldflags="+flags, ".")
	command.Env = append(os.Environ(), "DOMAINRY_RELEASE_LINKER_CHILD=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("linker injection child failed: %v\n%s", err, output)
	}
}

func setRuntimeReleaseLinkerFactsForTest() func() {
	previous := []string{
		builtProjectModule, builtVerificationReceiptSHA256, builtProjectInputSHA256, builtProjectSourceSHA256,
		builtGeneratedSDKSHA256, builtHandlerCatalogSHA256, builtSigningKeyID, builtSigningPublicKeySHA256,
		builtSigningPublicKeyBase64,
	}
	builtProjectModule = "example.com/project"
	builtVerificationReceiptSHA256 = strings.Repeat("1", 64)
	builtProjectInputSHA256 = strings.Repeat("2", 64)
	builtProjectSourceSHA256 = strings.Repeat("3", 64)
	builtGeneratedSDKSHA256 = validOptions().Identity.DomainSDK.ArtifactSHA256
	builtHandlerCatalogSHA256 = strings.Repeat("e", 64)
	builtSigningKeyID = "project-release"
	publicKey := []byte("01234567890123456789012345678901")
	publicKeyDigest := sha256.Sum256(publicKey)
	builtSigningPublicKeySHA256 = hex.EncodeToString(publicKeyDigest[:])
	builtSigningPublicKeyBase64 = base64.StdEncoding.EncodeToString(publicKey)
	return func() {
		builtProjectModule, builtVerificationReceiptSHA256, builtProjectInputSHA256, builtProjectSourceSHA256 = previous[0], previous[1], previous[2], previous[3]
		builtGeneratedSDKSHA256, builtHandlerCatalogSHA256, builtSigningKeyID, builtSigningPublicKeySHA256 = previous[4], previous[5], previous[6], previous[7]
		builtSigningPublicKeyBase64 = previous[8]
	}
}
