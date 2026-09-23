package runtimehost

import (
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

var (
	ErrRuntimeVersionRequired    = errors.New("runtime version is required")
	ErrRuntimeextVersionMismatch = errors.New("runtimeext contract version mismatch")
	ErrConnectorVersionMismatch  = errors.New("connector contract version mismatch")
)

// BuildIdentity is supplied by project composition and checked before
// Runtime configuration, persistence or network startup begins.
type BuildIdentity struct {
	RuntimeVersion            string
	RuntimeextContractVersion string
	ConnectorContractVersion  string
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
	return nil
}
