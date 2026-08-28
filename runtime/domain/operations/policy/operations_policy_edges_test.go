package policy

import (
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func validOperationsCommand(now time.Time) operationsmodel.OperationsCommand {
	return operationsmodel.OperationsCommand{
		ID: "operation", Kind: "scheduler.retry", Permission: "scheduler.retry",
		Scope:          operationsmodel.OperationsScope{WorkspaceID: "workspace", ResourceType: "run", ResourceID: "run-1"},
		IdempotencyKey: "key", RequestFingerprint: "fingerprint", RequestedBy: "operator",
		Reason: "recover", Reference: "INC-1", Status: operationsmodel.OperationsStatusCreated,
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestOperationsValidateCommandCompleteMatrix(t *testing.T) {
	now := time.Now().UTC()
	base := validOperationsCommand(now)
	for _, test := range []struct {
		name   string
		mutate func(*operationsmodel.OperationsCommand)
	}{
		{name: "id", mutate: func(command *operationsmodel.OperationsCommand) { command.ID = "" }},
		{name: "kind", mutate: func(command *operationsmodel.OperationsCommand) { command.Kind = "" }},
		{name: "permission", mutate: func(command *operationsmodel.OperationsCommand) { command.Permission = "" }},
		{name: "idempotency", mutate: func(command *operationsmodel.OperationsCommand) { command.IdempotencyKey = "" }},
		{name: "fingerprint", mutate: func(command *operationsmodel.OperationsCommand) { command.RequestFingerprint = "" }},
		{name: "requester", mutate: func(command *operationsmodel.OperationsCommand) { command.RequestedBy = "" }},
		{name: "reason", mutate: func(command *operationsmodel.OperationsCommand) { command.Reason = "" }},
		{name: "resource type", mutate: func(command *operationsmodel.OperationsCommand) { command.Scope.ResourceType = "" }},
		{name: "no scope", mutate: func(command *operationsmodel.OperationsCommand) { command.Scope.WorkspaceID = "" }},
		{name: "both scopes", mutate: func(command *operationsmodel.OperationsCommand) { command.Scope.SystemPurpose = "recovery" }},
		{name: "status", mutate: func(command *operationsmodel.OperationsCommand) {
			command.Status = operationsmodel.OperationsStatusStarted
		}},
		{name: "created", mutate: func(command *operationsmodel.OperationsCommand) { command.CreatedAt = time.Time{} }},
		{name: "updated", mutate: func(command *operationsmodel.OperationsCommand) { command.UpdatedAt = time.Time{} }},
		{name: "timestamps differ", mutate: func(command *operationsmodel.OperationsCommand) { command.UpdatedAt = now.Add(time.Second) }},
		{name: "started", mutate: func(command *operationsmodel.OperationsCommand) { command.StartedAt = &now }},
		{name: "finished", mutate: func(command *operationsmodel.OperationsCommand) { command.FinishedAt = &now }},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := base
			test.mutate(&command)
			if err := OperationsValidateCommand(command); err == nil {
				t.Fatal("expected invalid command")
			}
		})
	}
	system := base
	system.Scope.WorkspaceID, system.Scope.SystemPurpose = "", "disaster recovery"
	if err := OperationsValidateCommand(system); err != nil {
		t.Fatalf("system-scoped command: %v", err)
	}
}

func TestOperationsValidateReceiptStatusMatrix(t *testing.T) {
	now, finished := time.Now().UTC(), time.Now().UTC().Add(time.Second)
	base := operationsmodel.OperationsReceipt{StatusURL: "/operations/1"}
	for _, receipt := range []operationsmodel.OperationsReceipt{
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusCreated}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusStarted, StartedAt: &now}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusSucceeded, StartedAt: &now, FinishedAt: &finished}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusFailed, StartedAt: &now, FinishedAt: &finished}, ErrorCode: "provider.timeout", FailureClass: operationsmodel.OperationsFailureRetryable},
	} {
		if err := OperationsValidateReceipt(receipt); err != nil {
			t.Fatalf("valid receipt %+v: %v", receipt, err)
		}
	}
	invalid := []operationsmodel.OperationsReceipt{
		{Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusCreated}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusCreated, StartedAt: &now}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusCreated, FinishedAt: &finished}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusStarted}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusStarted, StartedAt: &now, FinishedAt: &finished}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusSucceeded, FinishedAt: &finished}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusSucceeded, StartedAt: &now}},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusSucceeded, StartedAt: &now, FinishedAt: &finished}, FailureClass: operationsmodel.OperationsFailureTerminal},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusSucceeded, StartedAt: &now, FinishedAt: &finished}, ErrorCode: "error"},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusFailed, FinishedAt: &finished}, ErrorCode: "error", FailureClass: operationsmodel.OperationsFailureTerminal},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusFailed, StartedAt: &now}, ErrorCode: "error", FailureClass: operationsmodel.OperationsFailureTerminal},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusFailed, StartedAt: &now, FinishedAt: &finished}, FailureClass: operationsmodel.OperationsFailureTerminal},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: operationsmodel.OperationsStatusFailed, StartedAt: &now, FinishedAt: &finished}, ErrorCode: "error", FailureClass: "unknown"},
		{StatusURL: base.StatusURL, Command: operationsmodel.OperationsCommand{Status: "unknown"}},
	}
	for index, receipt := range invalid {
		if err := OperationsValidateReceipt(receipt); err == nil {
			t.Fatalf("invalid receipt %d accepted: %+v", index, receipt)
		}
	}
}

func TestOperationsClassifySubmissionShortCircuitOutcomes(t *testing.T) {
	existing := &operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{IdempotencyKey: "original", RequestFingerprint: "same"}}
	if decision := OperationsClassifySubmission(existing, operationsmodel.OperationsCommand{IdempotencyKey: "different", RequestFingerprint: "same"}); decision != operationsmodel.OperationsSubmissionConflict {
		t.Fatalf("different key decision = %s", decision)
	}
}

func validCrossWorkspaceCommand(now time.Time) operationsmodel.CrossWorkspaceCommand {
	return operationsmodel.CrossWorkspaceCommand{
		ID: "operation", Purpose: operationsmodel.CrossWorkspaceSupport, Permission: "runtime.cross_workspace.support",
		SourceWorkspaceID: "workspace-a", TargetWorkspaceID: "workspace-b", ActorID: "operator",
		Reason: "support", Reference: "INC-1", RequestedAt: now,
	}
}

func TestCrossWorkspaceAndBreakGlassCompleteMatrix(t *testing.T) {
	now := time.Now().UTC()
	base := validCrossWorkspaceCommand(now)
	for _, test := range []struct {
		name   string
		mutate func(*operationsmodel.CrossWorkspaceCommand)
	}{
		{name: "unknown purpose", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.Purpose = "unknown" }},
		{name: "permission", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.Permission = "wrong" }},
		{name: "id", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.ID = "" }},
		{name: "source", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.SourceWorkspaceID = "" }},
		{name: "target", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.TargetWorkspaceID = "" }},
		{name: "same workspace", mutate: func(command *operationsmodel.CrossWorkspaceCommand) {
			command.TargetWorkspaceID = command.SourceWorkspaceID
		}},
		{name: "source wildcard", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.SourceWorkspaceID = "*" }},
		{name: "target wildcard", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.TargetWorkspaceID = "*" }},
		{name: "actor", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.ActorID = "" }},
		{name: "reason", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.Reason = "" }},
		{name: "reference", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.Reference = "" }},
		{name: "requested at", mutate: func(command *operationsmodel.CrossWorkspaceCommand) { command.RequestedAt = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := base
			test.mutate(&command)
			if err := OperationsValidateCrossWorkspaceCommand(command, now); err == nil {
				t.Fatal("expected invalid cross-workspace command")
			}
		})
	}
	validGrant := operationsmodel.BreakGlassGrant{AuditEventID: "audit", ExpiresAt: now.Add(30 * time.Minute), ApproverIDs: []string{"approver-a", "approver-b"}}
	for _, test := range []struct {
		name   string
		mutate func(*operationsmodel.BreakGlassGrant)
	}{
		{name: "audit", mutate: func(grant *operationsmodel.BreakGlassGrant) { grant.AuditEventID = "" }},
		{name: "zero expiry", mutate: func(grant *operationsmodel.BreakGlassGrant) { grant.ExpiresAt = time.Time{} }},
		{name: "expired", mutate: func(grant *operationsmodel.BreakGlassGrant) { grant.ExpiresAt = now }},
		{name: "too long", mutate: func(grant *operationsmodel.BreakGlassGrant) {
			grant.ExpiresAt = now.Add(MaximumBreakGlassDuration + time.Second)
		}},
		{name: "empty approvers", mutate: func(grant *operationsmodel.BreakGlassGrant) { grant.ApproverIDs = []string{"", " "} }},
		{name: "actor approval", mutate: func(grant *operationsmodel.BreakGlassGrant) { grant.ApproverIDs = []string{"operator", "approver"} }},
		{name: "duplicates", mutate: func(grant *operationsmodel.BreakGlassGrant) { grant.ApproverIDs = []string{"approver", " approver "} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			grant := validGrant
			test.mutate(&grant)
			if err := OperationsValidateBreakGlass(grant, "operator", now); err == nil {
				t.Fatal("expected invalid break-glass grant")
			}
		})
	}
	base.BreakGlass = &validGrant
	if err := OperationsValidateCrossWorkspaceCommand(base, now); err != nil {
		t.Fatalf("valid break glass: %v", err)
	}
}

func TestWorkspaceDeletionAndLeaseReleaseCompleteMatrix(t *testing.T) {
	now := time.Now().UTC()
	base := operationsmodel.WorkspaceDeletion{WorkspaceID: "workspace", ActorID: "operator", Reason: "closure", Reference: "REQ-1", UpdatedAt: now}
	current := base
	current.State = operationsmodel.WorkspaceDeletionActive
	next := base
	next.State = operationsmodel.WorkspaceDeletionFrozen
	for _, mutate := range []func(*operationsmodel.WorkspaceDeletion, *operationsmodel.WorkspaceDeletion){
		func(current, _ *operationsmodel.WorkspaceDeletion) { current.WorkspaceID = "" },
		func(_, next *operationsmodel.WorkspaceDeletion) { next.WorkspaceID = "other" },
		func(_, next *operationsmodel.WorkspaceDeletion) { next.ActorID = "" },
		func(_, next *operationsmodel.WorkspaceDeletion) { next.Reason = "" },
		func(_, next *operationsmodel.WorkspaceDeletion) { next.Reference = "" },
		func(_, next *operationsmodel.WorkspaceDeletion) { next.UpdatedAt = time.Time{} },
	} {
		invalidCurrent, invalidNext := current, next
		mutate(&invalidCurrent, &invalidNext)
		if err := OperationsTransitionWorkspaceDeletion(invalidCurrent, invalidNext, now); err == nil {
			t.Fatal("expected invalid deletion audit context")
		}
	}
	if err := OperationsTransitionWorkspaceDeletion(current, next, now); err != nil {
		t.Fatal(err)
	}
	frozen := next
	exported := base
	exported.State = operationsmodel.WorkspaceDeletionExported
	if err := OperationsTransitionWorkspaceDeletion(frozen, exported, now); err == nil {
		t.Fatal("export without evidence accepted")
	}
	exported.ExportReference = "export://1"
	if err := OperationsTransitionWorkspaceDeletion(frozen, exported, now); err != nil {
		t.Fatal(err)
	}
	retained := base
	retained.State = operationsmodel.WorkspaceDeletionRetained
	for _, retention := range []time.Time{{}, now, now.Add(-time.Second)} {
		retained.RetentionUntil = retention
		if err := OperationsTransitionWorkspaceDeletion(exported, retained, now); err == nil {
			t.Fatal("invalid retention window accepted")
		}
	}
	retained.RetentionUntil = now.Add(time.Hour)
	if err := OperationsTransitionWorkspaceDeletion(exported, retained, now); err != nil {
		t.Fatal(err)
	}
	cleanup := base
	cleanup.State = operationsmodel.WorkspaceDeletionCleanupPending
	for _, mutate := range []func(*operationsmodel.WorkspaceDeletion){
		func(current *operationsmodel.WorkspaceDeletion) { current.LegalHold = true },
		func(current *operationsmodel.WorkspaceDeletion) { current.RetentionUntil = time.Time{} },
		func(current *operationsmodel.WorkspaceDeletion) { current.RetentionUntil = now.Add(time.Second) },
	} {
		blocked := retained
		mutate(&blocked)
		if err := OperationsTransitionWorkspaceDeletion(blocked, cleanup, now); err == nil {
			t.Fatal("blocked cleanup accepted")
		}
	}
	retained.RetentionUntil = now
	if err := OperationsTransitionWorkspaceDeletion(retained, cleanup, now); err != nil {
		t.Fatal(err)
	}
	deleted := base
	deleted.State = operationsmodel.WorkspaceDeletionDeleted
	if err := OperationsTransitionWorkspaceDeletion(cleanup, deleted, now); err == nil {
		t.Fatal("deletion without cleanup evidence accepted")
	}
	deleted.CleanupEvidence = "cleanup-1"
	if err := OperationsTransitionWorkspaceDeletion(cleanup, deleted, now); err != nil {
		t.Fatal(err)
	}
	if err := OperationsTransitionWorkspaceDeletion(current, deleted, now); err == nil {
		t.Fatal("invalid deletion transition accepted")
	}
	for _, wrongCurrent := range []operationsmodel.WorkspaceDeletion{frozen, exported, retained, cleanup} {
		wrongNext := base
		wrongNext.State = wrongCurrent.State
		if err := OperationsTransitionWorkspaceDeletion(wrongCurrent, wrongNext, now); err == nil {
			t.Fatalf("same-state deletion transition %s accepted", wrongCurrent.State)
		}
	}

	lease := operationsmodel.OperationsLeaseReleaseRequest{Owner: "scheduler", ResourceID: "run", ExpectedLeaseOwner: "worker", ExpectedFencingToken: 1, Now: now}
	if err := OperationsValidateLeaseReleaseRequest(lease); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*operationsmodel.OperationsLeaseReleaseRequest){
		func(request *operationsmodel.OperationsLeaseReleaseRequest) { request.Owner = "" },
		func(request *operationsmodel.OperationsLeaseReleaseRequest) { request.ResourceID = "" },
		func(request *operationsmodel.OperationsLeaseReleaseRequest) { request.ExpectedLeaseOwner = "" },
		func(request *operationsmodel.OperationsLeaseReleaseRequest) { request.ExpectedFencingToken = 0 },
		func(request *operationsmodel.OperationsLeaseReleaseRequest) { request.Now = time.Time{} },
	} {
		invalid := lease
		mutate(&invalid)
		if err := OperationsValidateLeaseReleaseRequest(invalid); err == nil {
			t.Fatal("invalid lease release accepted")
		}
	}
	lease.VerifiedStuck = true
	if err := OperationsValidateLeaseReleaseRequest(lease); err == nil {
		t.Fatal("verified stuck lease without evidence accepted")
	}
	lease.VerificationEvidence = "heartbeat expired"
	if err := OperationsValidateLeaseReleaseRequest(lease); err != nil {
		t.Fatal(err)
	}
}
