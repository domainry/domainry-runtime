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

func (h releaseIdentityTestHandler) Descriptor() runtimeext.HandlerDescriptor {
	return runtimeext.HandlerDescriptor{
		ActionKey: "booking.reserve", InputType: "generated/booking.ReserveInput", OutputType: "generated/booking.ReserveOutput",
		InputContractSHA256: strings.Repeat("a", 64), OutputContractSHA256: strings.Repeat("b", 64), HandlerRevision: h.revision,
	}
}

func (releaseIdentityTestHandler) Invoke(context.Context, runtimeext.ActionExecution, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func TestRuntimeReleaseIdentityPublishesDeterministicComposition(t *testing.T) {
	handlers := runtimeext.NewBusinessHandlerRegistry()
	if err := handlers.Register(releaseIdentityTestHandler{revision: "revision-1"}); err != nil {
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
	if first != second || first.ContractVersion != RuntimeReleaseIdentityVersion || first.BuildMode != "development" || !lowerSHA256(first.HandlerRegistrySHA256) || !lowerSHA256(first.ConnectorRegistrySHA256) || !lowerSHA256(first.CombinationSHA256) {
		t.Fatalf("release identity=%+v second=%+v", first, second)
	}

	changedHandlers := runtimeext.NewBusinessHandlerRegistry()
	if err := changedHandlers.Register(releaseIdentityTestHandler{revision: "revision-2"}); err != nil {
		t.Fatal(err)
	}
	changedHandlers.Freeze()
	changed, err := runtimeReleaseIdentity(validOptions().Identity, changedHandlers, connectors)
	if err != nil {
		t.Fatal(err)
	}
	if changed.HandlerRegistrySHA256 == first.HandlerRegistrySHA256 || changed.CombinationSHA256 == first.CombinationSHA256 {
		t.Fatalf("registry drift did not change combination: first=%+v changed=%+v", first, changed)
	}
}

func TestRuntimeReleaseIdentityValidatesPackagedLinkerFacts(t *testing.T) {
	restore := setRuntimeReleaseLinkerFactsForTest()
	defer restore()
	handlers := runtimeext.NewBusinessHandlerRegistry()
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
