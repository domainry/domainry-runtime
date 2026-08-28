package integration

import (
	"testing"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationvalidation "github.com/domainry/domainry-runtime/runtime/domain/integration/validation"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestIntegrationReadExamplesExecuteOwnerValidator(t *testing.T) {
	for _, capability := range integrationcontract.IntegrationDeliveryAuthoringCapabilities() {
		if capability.Key != "integration.invocation.list" && capability.Key != "integration.event.list" && capability.Key != "integration.outbox.list" {
			continue
		}
		for _, example := range capability.Examples {
			limit, _ := example.Value["limit"].(int)
			if limit == 0 {
				limit = 1
			}
			maximum := 200
			if capability.Key == "integration.event.list" {
				maximum = 100
			}
			err := integrationvalidation.IntegrationValidateReadLimit(limit, maximum)
			if len(example.ExpectedErrorCodes) == 0 && err != nil {
				t.Fatalf("capability=%s example=%s err=%v", capability.Key, example.Name, err)
			}
			if len(example.ExpectedErrorCodes) > 0 && apperror.CodeOf(err) != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s code=%s want=%s", capability.Key, example.Name, apperror.CodeOf(err), example.ExpectedErrorCodes[0])
			}
		}
	}
}
