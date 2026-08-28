package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type operationsDiagnosticsProbe struct {
	calls   int
	err     error
	request operationsmodel.OperationsDiagnosticsRequest
}

func (p *operationsDiagnosticsProbe) OperationsDiagnosticsSnapshot(_ context.Context, request operationsmodel.OperationsDiagnosticsRequest) (operationsmodel.OperationsDiagnosticsSnapshot, error) {
	p.calls++
	p.request = request
	if p.err != nil {
		return operationsmodel.OperationsDiagnosticsSnapshot{}, p.err
	}
	return operationsmodel.OperationsDiagnosticsSnapshot{CapturedAt: request.Now, Redacted: true, Page: request.Page, PageSize: request.PageSize, CostUnits: len(request.Sections) * request.PageSize, Sections: map[string]operationsmodel.OperationsDiagnosticsSection{"db_pool": {Status: "ready"}}}, nil
}

var errOperationsDiagnosticsProbe = errors.New("diagnostics probe failed")

func TestOperationsDiagnosticsIsBoundedAndReceiptReplayDoesNotRecapture(t *testing.T) {
	var nilService *OperationsApplicationService
	nilService.UseDirectAuthoringProjection(nil)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, func() time.Time { return now }, func() string { return "diagnostics" })
	probe := &operationsDiagnosticsProbe{}
	if err := service.RegisterDiagnostics(probe, "runtime-1"); err != nil {
		t.Fatal(err)
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	command := OperationsDiagnosticsCommand{Sections: []string{"db_pool"}, PageSize: 10, Reason: "incident diagnostics"}
	first, err := service.CaptureDiagnostics(t.Context(), command, "diagnostics-key", principal)
	if err != nil || probe.calls != 1 || !first.Snapshot.Redacted || first.Receipt.Correlation == "" || len(first.Receipt.Evidence) == 0 || first.Receipt.NextAction == "" {
		t.Fatalf("first=%#v calls=%d err=%v", first, probe.calls, err)
	}
	replay, err := service.CaptureDiagnostics(t.Context(), command, "diagnostics-key", principal)
	if err != nil || probe.calls != 1 || replay.Receipt.Command.ID != first.Receipt.Command.ID {
		t.Fatalf("replay=%#v calls=%d err=%v", replay, probe.calls, err)
	}
	if _, err := service.CaptureDiagnostics(t.Context(), OperationsDiagnosticsCommand{Sections: []string{"db_pool"}, PageSize: 51, Reason: "too much"}, "large", principal); apperror.CodeOf(err) != "backend.operations.diagnostics_cost_exceeded" {
		t.Fatalf("cost err=%v", err)
	}
	if _, err := service.CaptureDiagnostics(t.Context(), OperationsDiagnosticsCommand{Sections: []string{"sql"}, Reason: "unsafe"}, "sql", principal); apperror.CodeOf(err) != "backend.operations.diagnostics_section_invalid" {
		t.Fatalf("section err=%v", err)
	}
}
