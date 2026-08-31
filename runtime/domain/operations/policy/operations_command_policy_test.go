package policy

import (
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestOperationsValidateCommandRequiresAuthorizationScopeIdempotencyAndAudit(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	valid := operationsmodel.OperationsCommand{
		ID: " op-1 ", Kind: " scheduler.run.retry ", Permission: " scheduler.runs.retry ",
		Scope:          operationsmodel.OperationsScope{WorkspaceID: " workspace-a ", ResourceType: " scheduler_run ", ResourceID: " run-1 "},
		IdempotencyKey: " retry-1 ", RequestFingerprint: " fingerprint-1 ", RequestedBy: " user-1 ",
		Reason: " recover transient failure ", Reference: " INC-42 ", Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now,
	}
	if err := OperationsValidateCommand(valid); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*operationsmodel.OperationsCommand)
	}{
		{name: "permission", mutate: func(command *operationsmodel.OperationsCommand) { command.Permission = "" }},
		{name: "idempotency", mutate: func(command *operationsmodel.OperationsCommand) { command.IdempotencyKey = "" }},
		{name: "fingerprint", mutate: func(command *operationsmodel.OperationsCommand) { command.RequestFingerprint = "" }},
		{name: "actor", mutate: func(command *operationsmodel.OperationsCommand) { command.RequestedBy = "" }},
		{name: "reason", mutate: func(command *operationsmodel.OperationsCommand) { command.Reason = "" }},
		{name: "scope", mutate: func(command *operationsmodel.OperationsCommand) { command.Scope.WorkspaceID = "" }},
		{name: "ambiguous scope", mutate: func(command *operationsmodel.OperationsCommand) { command.Scope.SystemPurpose = "restore" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := valid
			test.mutate(&command)
			if err := OperationsValidateCommand(command); err == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}

func TestOperationsClassifySubmissionReplaysSamePayloadAndConflictsOnChange(t *testing.T) {
	existing := &operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{IdempotencyKey: "operation-key", RequestFingerprint: "same"}}
	if decision := OperationsClassifySubmission(nil, operationsmodel.OperationsCommand{}); decision != operationsmodel.OperationsSubmissionAccepted {
		t.Fatalf("new command decision=%s", decision)
	}
	if decision := OperationsClassifySubmission(existing, operationsmodel.OperationsCommand{IdempotencyKey: " operation-key ", RequestFingerprint: " same "}); decision != operationsmodel.OperationsSubmissionReplay {
		t.Fatalf("same payload decision=%s", decision)
	}
	if decision := OperationsClassifySubmission(existing, operationsmodel.OperationsCommand{IdempotencyKey: "operation-key", RequestFingerprint: "changed"}); decision != operationsmodel.OperationsSubmissionConflict {
		t.Fatalf("changed payload decision=%s", decision)
	}
}

func TestOperationsValidateReceiptRequiresExplicitFailureRecovery(t *testing.T) {
	now, finished := time.Now().UTC(), time.Now().UTC().Add(time.Second)
	receipt := operationsmodel.OperationsReceipt{
		Command:   operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusFailed, StartedAt: &now, FinishedAt: &finished},
		StatusURL: "/operations/op-1", ErrorCode: "backend.provider.timeout", FailureClass: operationsmodel.OperationsFailureRetryable,
	}
	if err := OperationsValidateReceipt(receipt); err != nil {
		t.Fatalf("valid failure receipt rejected: %v", err)
	}
	for _, class := range []operationsmodel.OperationsFailureClass{
		operationsmodel.OperationsFailureRetryable,
		operationsmodel.OperationsFailureTerminal,
		operationsmodel.OperationsFailureManualIntervention,
	} {
		receipt.FailureClass = class
		if err := OperationsValidateReceipt(receipt); err != nil {
			t.Fatalf("failure class %s rejected: %v", class, err)
		}
	}
	receipt.FailureClass = ""
	if err := OperationsValidateReceipt(receipt); err == nil {
		t.Fatal("failed receipt without recovery class accepted")
	}
}
