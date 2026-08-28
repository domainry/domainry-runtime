package integrationcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

const ConnectorContractVersion = "1"

func ConnectorContractHash(connectors []integrationmodel.ConnectorSchema) (string, error) {
	raw, err := json.Marshal(connectors)
	if err != nil {
		return "", fmt.Errorf("encode integration connector contract: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
