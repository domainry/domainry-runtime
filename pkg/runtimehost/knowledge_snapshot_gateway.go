package runtimehost

import (
	"context"
	"strings"

	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

// KnowledgeDocumentSnapshotRequest checks current, owner-local access to an
// exact managed document. It carries no content, storage address or credential.
type KnowledgeDocumentSnapshotRequest struct {
	LibraryID string
	Snapshot  knowledgesdk.KnowledgeDocumentSnapshot
}

// KnowledgeDocumentSnapshotGateway is an optional composition capability.
// Project Handler logic receives a narrow, operation-specific adapter instead
// of the gateway or Knowledge owner service. It is metadata-only and prewrite;
// it never performs remote source search/fetch or approves customer disclosure.
type KnowledgeDocumentSnapshotGateway interface {
	CheckKnowledgeDocumentSnapshot(context.Context, runtimeext.ActionExecution, KnowledgeDocumentSnapshotRequest) (KnowledgeDocumentResult, error)
}

func CheckKnowledgeDocumentSnapshot(ctx context.Context, gateway ConnectorGateway, execution runtimeext.ActionExecution, request KnowledgeDocumentSnapshotRequest) (KnowledgeDocumentResult, error) {
	checker, ok := gateway.(KnowledgeDocumentSnapshotGateway)
	if !ok || checker == nil {
		return KnowledgeDocumentResult{}, snapshotGatewayUnavailable()
	}
	return checker.CheckKnowledgeDocumentSnapshot(ctx, execution, request)
}

func snapshotGatewayUnavailable() error {
	return &runtimeext.BusinessError{Class: runtimeext.BusinessErrorUnavailable, Code: "backend.knowledge.snapshot_check_unavailable", Message: "Managed Knowledge document snapshot checks are unavailable"}
}
func validateKnowledgeSnapshotRequest(execution runtimeext.ActionExecution, request KnowledgeDocumentSnapshotRequest) error {
	if execution == nil {
		return &runtimeext.BusinessError{Class: runtimeext.BusinessErrorInternal, Code: runtimeext.ConnectorActionExecutionRequiredErrorCode, Message: "Knowledge ActionExecution is required"}
	}
	if !execution.Principal().Known {
		return &runtimeext.BusinessError{Class: runtimeext.BusinessErrorForbidden, Code: "backend.knowledge.execution_identity_invalid", Message: "Knowledge snapshot checks require an authenticated Action principal"}
	}
	if request.LibraryID == "" || len(request.LibraryID) > 256 || request.LibraryID != strings.TrimSpace(request.LibraryID) || !request.Snapshot.Valid() {
		return &runtimeext.BusinessError{Class: runtimeext.BusinessErrorBadRequest, Code: "backend.knowledge.snapshot_request_invalid", Message: "Knowledge document snapshot request is invalid"}
	}
	return nil
}

func (gateway *bindableConnectorGateway) CheckKnowledgeDocumentSnapshot(ctx context.Context, execution runtimeext.ActionExecution, request KnowledgeDocumentSnapshotRequest) (KnowledgeDocumentResult, error) {
	if err := validateKnowledgeSnapshotRequest(execution, request); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	gateway.mu.RLock()
	target := gateway.target
	gateway.mu.RUnlock()
	checker, ok := target.(KnowledgeDocumentSnapshotGateway)
	if !ok || checker == nil {
		return KnowledgeDocumentResult{}, snapshotGatewayUnavailable()
	}
	return checker.CheckKnowledgeDocumentSnapshot(ctx, execution, request)
}

func (gateway integrationRuntimeConnectorGateway) CheckKnowledgeDocumentSnapshot(ctx context.Context, execution runtimeext.ActionExecution, request KnowledgeDocumentSnapshotRequest) (KnowledgeDocumentResult, error) {
	if err := validateKnowledgeSnapshotRequest(execution, request); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	authority, err := knowledgeDocumentAuthority(gateway.runtimeID, execution)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	checker, ok := gateway.knowledgeDocuments.(knowledgesdk.KnowledgeDocumentSnapshotService)
	if !ok || checker == nil {
		return KnowledgeDocumentResult{}, snapshotGatewayUnavailable()
	}
	document, err := checker.CheckKnowledgeDocumentSnapshot(ctx, request.LibraryID, request.Snapshot, authority)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	// The owner remains authoritative; do not accept a mismatched response or
	// fall back to a metadata Get when an optional capability is absent.
	if document.ID != request.Snapshot.DocumentID || document.LibraryID != request.LibraryID || document.ContentVersion != request.Snapshot.ContentVersion || document.IndexGeneration != request.Snapshot.IndexGeneration || document.SHA256 != request.Snapshot.SHA256 || document.OriginPageID != request.Snapshot.PageID || document.OriginPageVersion != request.Snapshot.PageVersion || document.State != "ready" || document.IndexStatus != "INDEXED" || document.SecurityStatus != "clean" || document.Reindexing || (document.ProjectionKind != "file" && document.ProjectionKind != "page") || (document.ProjectionKind == "page") != (request.Snapshot.PageID != "") {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Class: runtimeext.BusinessErrorConflict, Code: "backend.knowledge.snapshot_result_invalid", Message: "Knowledge document snapshot was not verified"}
	}
	return knowledgeDocumentResult(document), nil
}
