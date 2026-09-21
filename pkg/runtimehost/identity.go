package runtimehost

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

var (
	ErrRuntimeVersionRequired      = errors.New("runtime version is required")
	ErrRuntimeextVersionMismatch   = errors.New("runtimeext contract version mismatch")
	ErrConnectorVersionMismatch    = errors.New("connector contract version mismatch")
	ErrDomainSDKContractRequired   = errors.New("domain SDK contract identity is required")
	ErrDomainSDKGeneratorRequired  = errors.New("domain SDK generator version is required")
	ErrDomainSDKHashInvalid        = errors.New("domain SDK SHA-256 identity is invalid")
	ErrDomainSDKRuntimeextMismatch = errors.New("domain SDK runtimeext contract hash mismatch")
	ErrDomainSDKBuildMismatch      = errors.New("domain SDK build constraint mismatch")
	ErrDomainSDKTargetRequired     = errors.New("manifest domain SDK target identity is required")
	ErrDomainSDKTargetMismatch     = errors.New("compiled domain SDK does not match manifest target")
)

// DomainSDKIdentity binds one compiled generated SDK to its generator,
// published Metadata Snapshot, public Runtime contract and generated content.
type DomainSDKIdentity struct {
	ContractVersion                 string
	ContractSHA256                  string
	GeneratorVersion                string
	ApplicationSchemaSnapshotSHA256 string
	RuntimeextContractSHA256        string
	BuildConstraint                 string
	ArtifactSHA256                  string
}

// BuildIdentity is emitted by generated project composition and checked before
// Runtime configuration, persistence or network startup begins.
type BuildIdentity struct {
	RuntimeVersion            string
	RuntimeextContractVersion string
	ConnectorContractVersion  string
	DomainSDK                 DomainSDKIdentity
}

func (i BuildIdentity) Validate() error {
	if strings.TrimSpace(i.RuntimeVersion) == "" {
		return ErrRuntimeVersionRequired
	}
	if i.RuntimeextContractVersion != runtimeext.ContractVersion {
		return fmt.Errorf("%w: project=%q runtime=%q", ErrRuntimeextVersionMismatch, i.RuntimeextContractVersion, runtimeext.ContractVersion)
	}
	if i.ConnectorContractVersion != connector.ContractVersion {
		return fmt.Errorf("%w: project=%q runtime=%q", ErrConnectorVersionMismatch, i.ConnectorContractVersion, connector.ContractVersion)
	}
	// The repository's generic Runtime binary has no generated project SDK.
	// Project composition always supplies this identity and is validated below.
	if !i.DomainSDK.isZero() {
		if err := i.DomainSDK.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (i DomainSDKIdentity) isZero() bool {
	return i == (DomainSDKIdentity{})
}

func (i DomainSDKIdentity) Validate() error {
	if strings.TrimSpace(i.ContractVersion) == "" {
		return ErrDomainSDKContractRequired
	}
	if strings.TrimSpace(i.GeneratorVersion) == "" {
		return ErrDomainSDKGeneratorRequired
	}
	for name, value := range map[string]string{
		"contract": i.ContractSHA256, "metadata_snapshot": i.ApplicationSchemaSnapshotSHA256,
		"runtimeext": i.RuntimeextContractSHA256, "artifact": i.ArtifactSHA256,
	} {
		if !lowerSHA256(value) {
			return fmt.Errorf("%w: %s=%q", ErrDomainSDKHashInvalid, name, value)
		}
	}
	if i.RuntimeextContractSHA256 != runtimeext.ContractSHA256 {
		return fmt.Errorf("%w: sdk=%q runtime=%q", ErrDomainSDKRuntimeextMismatch, i.RuntimeextContractSHA256, runtimeext.ContractSHA256)
	}
	expected := domainSDKBuildConstraint(i)
	if i.BuildConstraint != expected {
		return fmt.Errorf("%w: sdk=%q expected=%q", ErrDomainSDKBuildMismatch, i.BuildConstraint, expected)
	}
	return nil
}

func domainSDKBuildConstraint(identity DomainSDKIdentity) string {
	payload := identity.ContractVersion + "\x00" + identity.ContractSHA256 + "\x00" + identity.GeneratorVersion + "\x00" + identity.ApplicationSchemaSnapshotSHA256 + "\x00" + identity.RuntimeextContractSHA256 + "\x00" + identity.ArtifactSHA256
	digest := sha256.Sum256([]byte(payload))
	return "domainry_domain_sdk_" + hex.EncodeToString(digest[:])
}

func lowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
