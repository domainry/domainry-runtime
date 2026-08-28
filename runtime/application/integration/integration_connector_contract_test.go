package integration

import (
	"testing"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestConnectorContractHashTracksProviderAndOperationChanges(t *testing.T) {
	base := []integrationmodel.ConnectorSchema{{Key: "payment", Type: "payment", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "stripe"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "charge"}}}}
	original, err := integrationcontract.ConnectorContractHash(base)
	if err != nil {
		t.Fatalf("hash base contract: %v", err)
	}
	changed := append([]integrationmodel.ConnectorSchema(nil), base...)
	changed[0].Providers = []integrationmodel.ConnectorProviderSchema{{Key: "paypal"}}
	providerHash, _ := integrationcontract.ConnectorContractHash(changed)
	changed[0] = base[0]
	changed[0].Operations = []integrationmodel.ConnectorOperationSchema{{Key: "refund"}}
	operationHash, _ := integrationcontract.ConnectorContractHash(changed)
	if original == providerHash || original == operationHash || providerHash == operationHash {
		t.Fatalf("contract hash did not distinguish provider/operation changes: %s %s %s", original, providerHash, operationHash)
	}
}
