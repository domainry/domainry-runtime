package runtimehost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

const RuntimeReleaseIdentityVersion = deploymentmodel.RuntimeReleaseIdentityVersion

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
		ProjectDefinitionRegistrySHA256: extensionHash, ConnectorRegistrySHA256: connectorHash,
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
