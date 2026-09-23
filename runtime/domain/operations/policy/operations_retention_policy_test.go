package policy

import (
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestOperationsRetentionDurationUsesTerminalStatusAndRejectsIncompleteRegistration(t *testing.T) {
	definition := operationsmodel.OperationsDefinition{Retention: operationsmodel.OperationsRetentionPolicy{
		PolicyKey: "operations.receipt.v1", Class: operationsmodel.OperationsRetentionLegalAudit,
		SucceededRetentionSeconds: int64((365 * 24 * time.Hour) / time.Second),
		FailedRetentionSeconds:    int64((7 * 365 * 24 * time.Hour) / time.Second),
		MinimumRetentionSeconds:   int64((90 * 24 * time.Hour) / time.Second),
	}}
	succeeded, err := OperationsRetentionDuration(definition, operationsmodel.OperationsStatusSucceeded)
	if err != nil || succeeded != 365*24*time.Hour {
		t.Fatalf("succeeded retention=%s err=%v", succeeded, err)
	}
	failed, err := OperationsRetentionDuration(definition, operationsmodel.OperationsStatusFailed)
	if err != nil || failed != 7*365*24*time.Hour {
		t.Fatalf("failed retention=%s err=%v", failed, err)
	}
	definition.Retention.SucceededRetentionSeconds = definition.Retention.MinimumRetentionSeconds - 1
	if _, err := OperationsRetentionDuration(definition, operationsmodel.OperationsStatusSucceeded); err == nil {
		t.Fatal("retention below registered minimum accepted")
	}
	if _, err := OperationsRetentionDuration(operationsmodel.OperationsDefinition{}, operationsmodel.OperationsStatusSucceeded); err == nil {
		t.Fatal("missing retention registration accepted")
	}
}
