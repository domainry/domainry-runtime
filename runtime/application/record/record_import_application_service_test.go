// Import application service tests.
package record

import (
	"encoding/json"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type importExecutionProbe struct {
	execution    recordmodel.RecordMutationExecution
	completed    bool
	allowReclaim bool
	beginErr     error
	lookupErr    error
}

type importClaimOnlyProbe struct{ target *importExecutionProbe }

func (p *importClaimOnlyProbe) TryBeginRecordMutation(ctx context.Context, request recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error) {
	return p.target.TryBeginRecordMutation(ctx, request)
}

func (p *importClaimOnlyProbe) CommitRecordMutationExecution(ctx context.Context, commit transactionmodel.RecordMutationCommit, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	return p.target.CommitRecordMutationExecution(ctx, commit, completion)
}

func (p *importClaimOnlyProbe) CompleteRecordMutationExecution(ctx context.Context, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	return p.target.CompleteRecordMutationExecution(ctx, completion)
}

func (p *importExecutionProbe) FindRecordMutationExecution(context.Context, recordmodel.RecordMutationExecution) (recordmodel.RecordMutationExecution, bool, error) {
	if p.lookupErr != nil {
		return recordmodel.RecordMutationExecution{}, false, p.lookupErr
	}
	return p.execution, p.execution.ID != "", nil
}

func (p *importExecutionProbe) TryBeginRecordMutation(_ context.Context, request recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error) {
	if p.beginErr != nil {
		return recordmodel.RecordMutationClaimResult{}, p.beginErr
	}
	if p.execution.ID == "" {
		p.execution = request.Execution
		p.execution.ID, p.execution.RequestFingerprint = "import-execution", request.RequestFingerprint
		p.execution.Status, p.execution.LeaseOwner, p.execution.FencingToken = string(idempotency.StatusProcessing), request.LeaseOwner, 1
		return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired, Execution: p.execution}, nil
	}
	if p.execution.RequestFingerprint != request.RequestFingerprint {
		return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionFingerprintConflict, Execution: p.execution}, nil
	}
	if p.completed {
		return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionReplay, Execution: p.execution}, nil
	}
	if p.allowReclaim {
		p.allowReclaim = false
		p.execution.LeaseOwner, p.execution.FencingToken = request.LeaseOwner, p.execution.FencingToken+1
		return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired, Execution: p.execution}, nil
	}
	return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionInProgress, Execution: p.execution}, nil
}

func TestImportIdempotentReplayStoreFailure(t *testing.T) {
	failure := errors.New("replay store unavailable")
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	service := NewRecordImportApplicationService(RecordImportDependencies{
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		Execution: recordruntime.NewRecordMutationExecutionRuntime(&importExecutionProbe{lookupErr: failure}),
		CreateIdempotent: func(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, bool, error) {
			return recordmodel.Record{}, false, nil
		},
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}})
	if _, _, err := service.ApplyIdempotent(t.Context(), object.Key, []byte("name\nAcme\n"), "key", principal); !errors.Is(err, failure) {
		t.Fatalf("err=%v", err)
	}
}

func TestImportIdempotentClaimStageReplayAndConflictEvidence(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}}
	probe := &importClaimOnlyProbe{target: &importExecutionProbe{}}
	created := map[string]bool{}
	service := NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{existing: map[string]bool{}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		Execution: recordruntime.NewRecordMutationExecutionRuntime(probe),
		CreateIdempotent: func(_ context.Context, _ string, data map[string]any, key string, _ principalmodel.Principal) (recordmodel.Record, bool, error) {
			replayed := created[key]
			created[key] = true
			return recordmodel.Record{ID: key, Data: data}, replayed, nil
		},
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}, RequestID: "request"})
	raw := []byte("name\nAcme\n")
	if _, replayed, err := service.ApplyIdempotent(t.Context(), object.Key, raw, "key", principal); err != nil || replayed {
		t.Fatalf("first replayed=%v err=%v", replayed, err)
	}
	if _, replayed, err := service.ApplyIdempotent(t.Context(), object.Key, raw, "key", principal); err != nil || !replayed {
		t.Fatalf("second replayed=%v err=%v", replayed, err)
	}
	if _, _, err := service.ApplyIdempotent(t.Context(), object.Key, []byte("name\nDifferent\n"), "key", principal); apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("conflict err=%v", err)
	}
}

func (p *importExecutionProbe) CommitRecordMutationExecution(context.Context, transactionmodel.RecordMutationCommit, recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	return recordmodel.RecordMutationExecution{}, errors.New("unexpected record commit")
}

func (p *importExecutionProbe) CompleteRecordMutationExecution(_ context.Context, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	raw, _ := json.Marshal(completion.Result)
	_ = json.Unmarshal(raw, &p.execution.OperationResult)
	p.execution.Status, p.completed = string(idempotency.StatusSucceeded), true
	return p.execution, nil
}

type importRepositoryProbe struct {
	recordrepository.RecordRepository
	existing map[string]bool
	err      error
}

func (r *importRepositoryProbe) UniqueExists(_ context.Context, _ string, objectKey, fieldKey, _ string, value any) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	return r.existing[objectKey+":"+fieldKey+":"+stringValue(value)], nil
}

func TestImportServiceBuildsRowLevelDuplicatePreview(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Name: "Name", Type: "text", Required: true, Unique: true},
		{Key: "status", Name: "Status", Type: "text"},
	}}
	repository := &importRepositoryProbe{existing: map[string]bool{}}
	service := NewRecordImportApplicationService(RecordImportDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		ValidateRelations: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
			return nil
		},
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})

	preview, err := service.Preview(t.Context(), "customer", []byte("Name,Status\nAcme,active\nAcme,prospect\n"), principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 2 || preview.ValidRows != 1 || preview.InvalidRows != 1 || preview.DuplicateRows != 1 || preview.CanApply || len(preview.ErrorRows) != 1 {
		t.Fatalf("preview = %#v", preview)
	}
	issue := preview.ErrorRows[0].Issues[len(preview.ErrorRows[0].Issues)-1]
	if issue.Code != "backend.import.duplicate_in_file" || issue.Field != "name" || issue.Params["row"] != "2" || preview.ErrorRows[0].ErrorSummary == "" {
		t.Fatalf("duplicate issue = %#v row=%#v", issue, preview.ErrorRows[0])
	}
}

func TestImportServiceConsumesStructuredDataExchangeRowsWithoutCSVRoundTrip(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Name: "Name", Type: "text", Required: true, Unique: true},
		{Key: "status", Name: "Status", Type: "text"},
	}}
	service := NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{existing: map[string]bool{}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	preview, err := service.PreviewRows(t.Context(), object.Key, []string{"Name", "Status"}, []dataexchange.ImportRow{
		{Number: 2, Values: []string{"Acme", "active"}},
		{Number: 3, Values: []string{"Acme", "prospect"}},
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 2 || preview.ValidRows != 1 || preview.InvalidRows != 1 || preview.DuplicateRows != 1 || preview.CanApply {
		t.Fatalf("structured preview=%#v", preview)
	}
	if _, err := service.PreviewRows(t.Context(), object.Key, []string{"Name"}, []dataexchange.ImportRow{{Number: 2, Values: []string{"Acme", "extra"}}}, principal); apperror.CodeOf(err) != "backend.import.invalid_csv" {
		t.Fatalf("invalid structured row err=%v", err)
	}
}

func TestImportServiceAppliesValidPreviewThroughCreatePort(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true, Unique: true}}}
	repository := &importRepositoryProbe{existing: map[string]bool{}}
	var created []map[string]any
	var events []string
	service := NewRecordImportApplicationService(RecordImportDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		CreateRecord: func(_ context.Context, _ string, data map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
			created = append(created, recordvalidation.RecordCloneData(data))
			return recordmodel.Record{ID: "created"}, nil
		},
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _, _ map[string]any) {
			events = append(events, event)
		},
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})

	result, err := service.Apply(t.Context(), "customer", []byte("name\nAcme\nBeta\n"), principal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 2 || result.Skipped != 0 || len(created) != 2 || created[0]["name"] != "Acme" || created[1]["name"] != "Beta" {
		t.Fatalf("result=%#v created=%#v", result, created)
	}
	if len(events) != 1 || events[0] != "record_import_applied" {
		t.Fatalf("events = %v", events)
	}
}

func TestImportServiceUsesOperationAndRowKeysForResumeReplayAndConflict(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true, Unique: true}}}
	executions := &importExecutionProbe{}
	completedRows := map[string]bool{}
	rowCalls := map[string]int{}
	failRowThree := true
	service := NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{existing: map[string]bool{}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		Execution: recordruntime.NewRecordMutationExecutionRuntime(executions),
		CreateIdempotent: func(_ context.Context, _ string, data map[string]any, key string, _ principalmodel.Principal) (recordmodel.Record, bool, error) {
			rowCalls[key]++
			if key == "import-1:row:3" && failRowThree {
				failRowThree = false
				return recordmodel.Record{}, false, errors.New("injected row failure")
			}
			if completedRows[key] {
				return recordmodel.Record{ID: key}, true, nil
			}
			completedRows[key] = true
			return recordmodel.Record{ID: key, Data: data}, false, nil
		},
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}, RequestID: "request-a"})
	rawCSV := []byte("name\nAcme\nBeta\n")
	if _, _, err := service.ApplyIdempotent(t.Context(), "customer", rawCSV, "import-1", principal); err == nil {
		t.Fatal("expected injected row failure")
	}
	executions.allowReclaim = true
	result, replayed, err := service.ApplyIdempotent(t.Context(), "customer", rawCSV, "import-1", principal)
	if err != nil || replayed || result.Created != 2 || len(completedRows) != 2 || rowCalls["import-1:row:2"] != 2 {
		t.Fatalf("resumed result=%#v replayed=%v completed=%v calls=%v err=%v", result, replayed, completedRows, rowCalls, err)
	}
	replayedResult, replayed, err := service.ApplyIdempotent(t.Context(), "customer", rawCSV, "import-1", principal)
	if err != nil || !replayed || replayedResult.Created != 2 || rowCalls["import-1:row:2"] != 2 {
		t.Fatalf("outer replay=%#v replayed=%v calls=%v err=%v", replayedResult, replayed, rowCalls, err)
	}
	_, _, err = service.ApplyIdempotent(t.Context(), "customer", []byte("name\nDifferent\n"), "import-1", principal)
	if apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("operation fingerprint conflict=%v", err)
	}
}

func TestImportServicePreservesPermissionAndRepositoryErrors(t *testing.T) {
	permissionErr := &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"}
	var event string
	service := NewRecordImportApplicationService(RecordImportDependencies{
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{}, permissionErr
		},
		Audit: func(_ context.Context, value, _, _ string, _ principalmodel.Principal, _ string, _, _, _ map[string]any) {
			event = value
		},
	})
	_, err := service.Preview(t.Context(), "customer", []byte("name\nAcme\n"), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if !errors.Is(err, permissionErr) || event != "record_import_denied" {
		t.Fatalf("permission error=%v event=%q", err, event)
	}

	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Unique: true}}}
	service = NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{err: errors.New("store unavailable")},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	_, err = service.Preview(t.Context(), "customer", []byte("name\nAcme\n"), recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	assertRecordApplicationError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check duplicate field"})
}

func TestImportServiceBoundsRowsColumnsBytesAndCancellation(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	service := NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{existing: map[string]bool{}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	var rows strings.Builder
	rows.WriteString("name\n")
	for index := 0; index <= recordImportMaxRows; index++ {
		fmt.Fprintf(&rows, "row-%d\n", index)
	}
	if _, err := service.Preview(t.Context(), "customer", []byte(rows.String()), principal); apperror.CodeOf(err) != "backend.import.too_many_rows" {
		t.Fatalf("row limit error=%v", err)
	}
	if _, err := service.Preview(t.Context(), "customer", make([]byte, recordImportMaxBytes+1), principal); apperror.CodeOf(err) != "backend.import.payload_too_large" {
		t.Fatalf("byte limit error=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Preview(ctx, "customer", []byte("name\nAcme\n"), principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
