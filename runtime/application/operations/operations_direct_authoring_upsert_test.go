package operations

import (
	"context"
	"errors"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestExecuteDirectAuthoringUpsertPersistsReplayAndChecksHashOnlyForOwnerExecution(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "authoring-upsert" })
	service.UseDirectAuthoringProjection(func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{SnapshotHash: "snapshot-1", AvailableSuccessors: []capabilitycontract.CapabilityAuthoringSuccessorSummary{{Key: "identity.role_permission", Domain: "identity", Status: "supported", DetailEndpoint: "/capabilities/identity.role_permission"}}}, nil
	})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "builder"}}
	ctx := operationscontract.WithBuilderTaskID(t.Context(), "task-1")
	request := DirectAuthoringUpsertRequest{CapabilityKey: "identity.role", ActionKey: "identity.roles.update", ResourceID: "manager", BuilderTaskID: "task-1", IdempotencyKey: "upsert-1", ExpectedResourceHash: "empty", Payload: map[string]any{"label": "Manager"}}
	currentHash, found, calls := "", false, 0

	invoke := func(request DirectAuthoringUpsertRequest) (OperationsOwnerExecutionResult, error) {
		return service.ExecuteDirectAuthoringUpsert(ctx, request, principal,
			func(context.Context) error { return nil },
			func(context.Context) (string, bool, error) { return currentHash, found, nil },
			func(context.Context) (any, error) {
				calls++
				currentHash, found = "hash-1", true
				return map[string]any{"id": "manager"}, nil
			},
		)
	}
	first, err := invoke(request)
	if err != nil || first.Replayed || first.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded || calls != 1 {
		t.Fatalf("first=%#v calls=%d err=%v", first, calls, err)
	}
	firstValue, ok := first.Value.(OperationsDirectAuthoringSuccessResult)
	if !ok || firstValue.ResourceHash != "hash-1" || firstValue.SnapshotHash != "snapshot-1" || len(firstValue.AvailableSuccessors) != 1 {
		t.Fatalf("first value=%#v", first.Value)
	}
	replay, err := invoke(request)
	if err != nil || !replay.Replayed || calls != 1 {
		t.Fatalf("replay=%#v calls=%d err=%v", replay, calls, err)
	}
	replayValue, ok := replay.Value.(map[string]any)
	if !ok || replayValue["resource_hash"] != "hash-1" || replayValue["snapshot_hash"] != "snapshot-1" {
		t.Fatalf("replay value=%#v", replay.Value)
	}
	changed := request
	changed.Payload = map[string]any{"label": "Changed"}
	if _, err := invoke(changed); apperror.CodeOf(err) != "backend.idempotency.key_reused" || calls != 1 {
		t.Fatalf("fingerprint conflict calls=%d err=%v", calls, err)
	}
	wrongHash := request
	wrongHash.IdempotencyKey = "upsert-2"
	wrongHash.ExpectedResourceHash = "old-hash"
	if _, err := invoke(wrongHash); apperror.CodeOf(err) != "backend.authoring.resource_hash_conflict" || calls != 1 {
		t.Fatalf("hash conflict calls=%d err=%v", calls, err)
	}
}

func TestExecuteDirectAuthoringUpsertRejectsHeaderAndOwnerContractViolations(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "authoring-invalid" })
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "builder"}}
	valid := DirectAuthoringUpsertRequest{CapabilityKey: "identity.role", ActionKey: "identity.roles.update", ResourceID: "manager", BuilderTaskID: "task-1", IdempotencyKey: "upsert-1", ExpectedResourceHash: "empty", Payload: map[string]any{}}
	call := func(ctx context.Context, request DirectAuthoringUpsertRequest, authorize func(context.Context) error) error {
		_, err := service.ExecuteDirectAuthoringUpsert(ctx, request, principal, authorize, func(context.Context) (string, bool, error) { return "", false, nil }, func(context.Context) (any, error) { return map[string]any{}, nil })
		return err
	}
	missingKey := valid
	missingKey.IdempotencyKey = ""
	if code := apperror.CodeOf(call(t.Context(), missingKey, func(context.Context) error { return nil })); code != "backend.idempotency.key_required" {
		t.Fatalf("missing key code=%s", code)
	}
	mismatched := operationscontract.WithBuilderTaskID(t.Context(), "other-task")
	if code := apperror.CodeOf(call(mismatched, valid, func(context.Context) error { return nil })); code != "backend.authoring.builder_task_mismatch" {
		t.Fatalf("task mismatch code=%s", code)
	}
	denied := apperror.New(apperror.KindForbidden, "auth.permission_denied", nil, nil)
	if code := apperror.CodeOf(call(t.Context(), valid, func(context.Context) error { return denied })); code != "auth.permission_denied" || len(repository.receipts) != 0 {
		t.Fatalf("authorization code=%s receipts=%d", code, len(repository.receipts))
	}
	if code := apperror.CodeOf(call(t.Context(), valid, func(context.Context) error { return nil })); code != "backend.authoring.success_projection_unavailable" || len(repository.receipts) != 0 {
		t.Fatalf("projection code=%s receipts=%d", code, len(repository.receipts))
	}
}

func TestExecuteDirectAuthoringUpsertHashesAnEmptyCollectionWithoutTreatingItAsExisting(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "authoring-empty-collection" })
	service.UseDirectAuthoringProjection(func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{SnapshotHash: "snapshot-empty", AvailableSuccessors: []capabilitycontract.CapabilityAuthoringSuccessorSummary{}}, nil
	})
	request := DirectAuthoringUpsertRequest{
		CapabilityKey: "identity.role_permission", ActionKey: "identity.roles.update", ResourceID: "viewer", BuilderTaskID: "task-1",
		IdempotencyKey: "empty-permissions", ExpectedResourceHash: "empty", Payload: map[string]any{"permission_keys": []any{}},
	}
	result, err := service.ExecuteDirectAuthoringUpsert(t.Context(), request, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "builder"}},
		func(context.Context) error { return nil },
		func(context.Context) (string, bool, error) { return "empty-set-hash", false, nil },
		func(context.Context) (any, error) { return []any{}, nil },
	)
	value, ok := result.Value.(OperationsDirectAuthoringSuccessResult)
	if err != nil || !ok || value.ResourceHash != "empty-set-hash" || value.SnapshotHash != "snapshot-empty" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestExecuteDirectAuthoringUpsertAllowsAbsentExpectedHashForFirstCreationOnly(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "builder"}}
	newService := func(key string) *OperationsApplicationService {
		service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return key })
		service.UseDirectAuthoringProjection(func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
			return capabilitycontract.CapabilityAuthoringSuccessProjection{SnapshotHash: "snapshot-1"}, nil
		})
		return service
	}
	request := DirectAuthoringUpsertRequest{
		CapabilityKey: "identity.menu", ActionKey: "identity.menus.update", ResourceID: "menu-1", BuilderTaskID: "task-1",
		IdempotencyKey: "first-create", ExpectedResourceHash: "", Payload: map[string]any{"key": "orders"},
	}
	authorize := func(context.Context) error { return nil }

	// A menu that does not exist yet has no resource hash to present; an
	// absent Expected-Schema-Hash must permit first creation.
	created := false
	result, err := newService("first-create").ExecuteDirectAuthoringUpsert(t.Context(), request, principal, authorize,
		func(context.Context) (string, bool, error) {
			if created {
				return "hash-1", true, nil
			}
			return "", false, nil
		},
		func(context.Context) (any, error) {
			created = true
			return map[string]any{"id": "menu-1"}, nil
		},
	)
	value, ok := result.Value.(OperationsDirectAuthoringSuccessResult)
	if err != nil || !created || !ok || value.ResourceHash != "hash-1" {
		t.Fatalf("first creation result=%#v created=%v err=%v", result, created, err)
	}

	// The same absent hash against an existing resource must keep failing the
	// optimistic concurrency check instead of overwriting it.
	executed := false
	conflict := request
	conflict.IdempotencyKey = "conflict"
	if _, err := newService("conflict").ExecuteDirectAuthoringUpsert(t.Context(), conflict, principal, authorize,
		func(context.Context) (string, bool, error) { return "hash-1", true, nil },
		func(context.Context) (any, error) {
			executed = true
			return map[string]any{}, nil
		},
	); apperror.CodeOf(err) != "backend.authoring.resource_hash_conflict" || executed {
		t.Fatalf("existing resource executed=%v err=%v", executed, err)
	}
}

func TestExecuteDirectAuthoringUpsertRemainingContractAndProjectionOutcomes(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "builder"}}
	valid := DirectAuthoringUpsertRequest{
		CapabilityKey: "identity.role", ActionKey: "identity.roles.update", ResourceID: "manager", BuilderTaskID: "task-1",
		IdempotencyKey: "upsert", ExpectedResourceHash: "empty", Payload: map[string]any{},
	}
	newService := func(key string) *OperationsApplicationService {
		return NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return key })
	}
	authorize := func(context.Context) error { return nil }
	currentHash := func(context.Context) (string, bool, error) { return "", false, nil }
	execute := func(context.Context) (any, error) { return map[string]any{}, nil }

	for _, test := range []struct {
		name   string
		mutate func(*DirectAuthoringUpsertRequest)
		code   string
	}{
		{name: "capability", mutate: func(request *DirectAuthoringUpsertRequest) { request.CapabilityKey = "" }, code: "backend.authoring.request_identity_required"},
		{name: "action", mutate: func(request *DirectAuthoringUpsertRequest) { request.ActionKey = "" }, code: "backend.authoring.request_identity_required"},
		{name: "resource", mutate: func(request *DirectAuthoringUpsertRequest) { request.ResourceID = "" }, code: "backend.authoring.request_identity_required"},
		{name: "builder task", mutate: func(request *DirectAuthoringUpsertRequest) { request.BuilderTaskID = "" }, code: "backend.authoring.request_identity_required"},
	} {
		t.Run("identity_"+test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if _, err := newService(test.name).ExecuteDirectAuthoringUpsert(t.Context(), request, principal, authorize, currentHash, execute); apperror.CodeOf(err) != test.code {
				t.Fatalf("error=%v", err)
			}
		})
	}

	for _, test := range []struct {
		name        string
		authorize   func(context.Context) error
		currentHash func(context.Context) (string, bool, error)
		execute     func(context.Context) (any, error)
	}{
		{name: "authorize", authorize: nil, currentHash: currentHash, execute: execute},
		{name: "current hash", authorize: authorize, currentHash: nil, execute: execute},
		{name: "execute", authorize: authorize, currentHash: currentHash, execute: nil},
	} {
		t.Run("contract_"+test.name, func(t *testing.T) {
			if _, err := newService(test.name).ExecuteDirectAuthoringUpsert(t.Context(), valid, principal, test.authorize, test.currentHash, test.execute); apperror.CodeOf(err) != "backend.authoring.owner_contract_unavailable" {
				t.Fatalf("error=%v", err)
			}
		})
	}

	fault := errors.New("fault")
	run := func(
		name string,
		expectedHash string,
		hash func(context.Context) (string, bool, error),
		projection func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error),
	) error {
		service := newService(name)
		service.UseDirectAuthoringProjection(projection)
		request := valid
		request.IdempotencyKey = name
		request.ExpectedResourceHash = expectedHash
		_, err := service.ExecuteDirectAuthoringUpsert(t.Context(), request, principal, authorize, hash, execute)
		return err
	}
	validProjection := func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{SnapshotHash: "snapshot"}, nil
	}
	if err := run("initial-hash-error", "empty", func(context.Context) (string, bool, error) {
		return "", false, fault
	}, validProjection); !errors.Is(err, fault) {
		t.Fatalf("initial hash error=%v", err)
	}
	if code := apperror.CodeOf(run("empty-found", "empty", func(context.Context) (string, bool, error) {
		return "existing", true, nil
	}, validProjection)); code != "backend.authoring.resource_hash_conflict" {
		t.Fatalf("empty-found code=%s", code)
	}
	if err := run("matching-hash", "resource-hash", func(context.Context) (string, bool, error) {
		return "resource-hash", true, nil
	}, validProjection); err != nil {
		t.Fatalf("matching hash error=%v", err)
	}
	executeFailureService := newService("execute-error")
	executeFailureService.UseDirectAuthoringProjection(validProjection)
	executeFailureRequest := valid
	executeFailureRequest.IdempotencyKey = "execute-error"
	if _, err := executeFailureService.ExecuteDirectAuthoringUpsert(
		t.Context(), executeFailureRequest, principal, authorize, currentHash,
		func(context.Context) (any, error) { return nil, fault },
	); !errors.Is(err, fault) {
		t.Fatalf("execute error=%v", err)
	}
	hashCalls := 0
	if err := run("final-hash-error", "empty", func(context.Context) (string, bool, error) {
		hashCalls++
		if hashCalls == 1 {
			return "", false, nil
		}
		return "", true, fault
	}, validProjection); !errors.Is(err, fault) {
		t.Fatalf("final hash error=%v", err)
	}
	hashCalls = 0
	if code := apperror.CodeOf(run("blank-resource-hash", "empty", func(context.Context) (string, bool, error) {
		hashCalls++
		return "", hashCalls > 1, nil
	}, validProjection)); code != "backend.authoring.resource_projection_unavailable" {
		t.Fatalf("blank resource hash code=%s", code)
	}
	if err := run("projection-error", "empty", func(context.Context) (string, bool, error) {
		return "resource-hash", false, nil
	}, func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{}, fault
	}); !errors.Is(err, fault) {
		t.Fatalf("projection error=%v", err)
	}
	if code := apperror.CodeOf(run("blank-snapshot", "empty", func(context.Context) (string, bool, error) {
		return "resource-hash", false, nil
	}, func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{}, nil
	})); code != "backend.authoring.snapshot_projection_unavailable" {
		t.Fatalf("blank snapshot code=%s", code)
	}

}
