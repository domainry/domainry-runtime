package runtimehost

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

const RuntimeReleaseIdentityVersion = deploymentmodel.RuntimeReleaseIdentityVersion

var (
	builtProjectModule             string
	builtVerificationReceiptSHA256 string
	builtProjectInputSHA256        string
	builtProjectSourceSHA256       string
	builtGeneratedSDKSHA256        string
	builtHandlerCatalogSHA256      string
	builtSigningKeyID              string
	builtSigningPublicKeySHA256    string
	builtSigningPublicKeyBase64    string
)

var ErrRuntimeReleaseIdentity = errors.New("runtime release identity is invalid")

func runtimeReleaseIdentity(build BuildIdentity, extensions *runtimeext.ProjectExtensionRegistry, connectors *connector.Registry) (runtimehttp.RuntimeReleaseIdentity, error) {
	extensionHash, err := runtimeRegistrySHA256("domainry-project-extension-registry-v1", extensions.Descriptors())
	if err != nil {
		return runtimehttp.RuntimeReleaseIdentity{}, err
	}
	connectorHash, err := runtimeRegistrySHA256("domainry-connector-registry-v1", connectors.Descriptors())
	if err != nil {
		return runtimehttp.RuntimeReleaseIdentity{}, err
	}
	identity := runtimehttp.RuntimeReleaseIdentity{
		ContractVersion: RuntimeReleaseIdentityVersion, BuildMode: "development",
		RuntimeVersion:            build.RuntimeVersion,
		RuntimeextContractVersion: build.RuntimeextContractVersion, RuntimeextContractSHA256: runtimeext.ContractSHA256,
		ConnectorContractVersion: build.ConnectorContractVersion, ConnectorContractSHA256: connector.ContractSHA256,
		DomainSDKContractVersion: build.DomainSDK.ContractVersion, DomainSDKContractSHA256: build.DomainSDK.ContractSHA256,
		DomainSDKGeneratorVersion: build.DomainSDK.GeneratorVersion, DomainSDKBuildConstraint: build.DomainSDK.BuildConstraint,
		ApplicationSchemaSnapshotSHA256: build.DomainSDK.ApplicationSchemaSnapshotSHA256, GeneratedSDKSHA256: build.DomainSDK.ArtifactSHA256,
		ProjectExtensionRegistrySHA256: extensionHash, ConnectorRegistrySHA256: connectorHash,
	}
	packaged := []string{
		builtProjectModule, builtVerificationReceiptSHA256, builtProjectInputSHA256, builtProjectSourceSHA256,
		builtGeneratedSDKSHA256, builtHandlerCatalogSHA256, builtSigningKeyID, builtSigningPublicKeySHA256,
		builtSigningPublicKeyBase64,
	}
	present := 0
	for _, value := range packaged {
		if strings.TrimSpace(value) != "" {
			present++
		}
	}
	if present != 0 && present != len(packaged) {
		return runtimehttp.RuntimeReleaseIdentity{}, fmt.Errorf("%w: packaged linker identity is incomplete", ErrRuntimeReleaseIdentity)
	}
	if present == len(packaged) {
		for name, value := range map[string]string{
			"verification receipt": builtVerificationReceiptSHA256, "project input": builtProjectInputSHA256,
			"project source": builtProjectSourceSHA256, "generated SDK": builtGeneratedSDKSHA256,
			"handler catalog": builtHandlerCatalogSHA256, "signing public key": builtSigningPublicKeySHA256,
		} {
			if !lowerSHA256(value) {
				return runtimehttp.RuntimeReleaseIdentity{}, fmt.Errorf("%w: %s hash is malformed", ErrRuntimeReleaseIdentity, name)
			}
		}
		if builtGeneratedSDKSHA256 != build.DomainSDK.ArtifactSHA256 {
			return runtimehttp.RuntimeReleaseIdentity{}, fmt.Errorf("%w: packaged and compiled generated SDK identities differ", ErrRuntimeReleaseIdentity)
		}
		if strings.TrimSpace(builtProjectModule) != builtProjectModule || builtProjectModule == "" || strings.TrimSpace(builtSigningKeyID) != builtSigningKeyID || builtSigningKeyID == "" {
			return runtimehttp.RuntimeReleaseIdentity{}, fmt.Errorf("%w: project module or signing key identity is malformed", ErrRuntimeReleaseIdentity)
		}
		publicKey, decodeErr := base64.StdEncoding.DecodeString(builtSigningPublicKeyBase64)
		publicKeyDigest := sha256.Sum256(publicKey)
		if decodeErr != nil || len(publicKey) != ed25519.PublicKeySize ||
			base64.StdEncoding.EncodeToString(publicKey) != builtSigningPublicKeyBase64 ||
			hex.EncodeToString(publicKeyDigest[:]) != builtSigningPublicKeySHA256 {
			return runtimehttp.RuntimeReleaseIdentity{}, fmt.Errorf("%w: packaged signing public key does not match its identity", ErrRuntimeReleaseIdentity)
		}
		identity.BuildMode = "packaged"
		identity.ProjectModule = builtProjectModule
		identity.VerificationReceiptSHA256 = builtVerificationReceiptSHA256
		identity.ProjectInputSHA256 = builtProjectInputSHA256
		identity.ProjectSourceSHA256 = builtProjectSourceSHA256
		identity.GeneratedSDKSHA256 = builtGeneratedSDKSHA256
		identity.HandlerCatalogSHA256 = builtHandlerCatalogSHA256
		identity.SigningKeyID = builtSigningKeyID
		identity.SigningPublicKeySHA256 = builtSigningPublicKeySHA256
	}
	identity.CombinationSHA256, err = runtimeReleaseCombinationSHA256(identity)
	if err != nil {
		return runtimehttp.RuntimeReleaseIdentity{}, err
	}
	return identity, nil
}

func runtimeRegistrySHA256(contractVersion string, descriptors any) (string, error) {
	digest, err := deploymentmodel.RuntimeRegistrySHA256(contractVersion, descriptors)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRuntimeReleaseIdentity, err)
	}
	return digest, nil
}

func runtimeReleaseCombinationSHA256(identity runtimehttp.RuntimeReleaseIdentity) (string, error) {
	identity.CombinationSHA256 = ""
	payload, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("%w: encode combination identity: %v", ErrRuntimeReleaseIdentity, err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
