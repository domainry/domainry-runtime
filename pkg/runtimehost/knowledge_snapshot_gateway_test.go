package runtimehost

import (
	"context"
	"errors"
	"strings"
	"testing"

	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

type snapshotOwnerProbe struct {
	knowledgesdk.KnowledgeDocumentService
	calls     int
	authority knowledgesdk.Authority
	library   string
	snapshot  knowledgesdk.KnowledgeDocumentSnapshot
	document  knowledgesdk.KnowledgeDocument
	err       error
}

type snapshotPhaseExecution struct {
	*integrationConnectorExecution
	phase runtimeext.ExecutionPhase
}

func (e snapshotPhaseExecution) Phase() runtimeext.ExecutionPhase { return e.phase }

func (p *snapshotOwnerProbe) CheckKnowledgeDocumentSnapshot(_ context.Context, library string, snapshot knowledgesdk.KnowledgeDocumentSnapshot, a knowledgesdk.Authority) (knowledgesdk.KnowledgeDocument, error) {
	p.calls++
	p.library, p.snapshot, p.authority = library, snapshot, a
	return p.document, p.err
}
func runtimeSnapshotFixture() (*integrationConnectorExecution, *snapshotOwnerProbe, KnowledgeDocumentSnapshotRequest) {
	execution := &integrationConnectorExecution{identity: runtimeext.ExecutionIdentity{ExecutionID: "review-1", ActionKey: "case.review", ObjectKey: "case", RecordID: "case-a"}, workspace: runtimeext.Workspace{ID: "workspace-a"}, principal: runtimeext.Principal{Known: true, UserID: "actual-reviewer", RoleKey: "support"}}
	request := KnowledgeDocumentSnapshotRequest{LibraryID: "library-a", Snapshot: knowledgesdk.KnowledgeDocumentSnapshot{DocumentID: "document-a", ContentVersion: 2, IndexGeneration: 3, SHA256: strings.Repeat("a", 64)}}
	probe := &snapshotOwnerProbe{document: knowledgesdk.KnowledgeDocument{ID: "document-a", LibraryID: "library-a", ProjectionKind: "file", ContentVersion: 2, IndexGeneration: 3, SHA256: request.Snapshot.SHA256, State: "ready", IndexStatus: "INDEXED", SecurityStatus: "clean", Revision: 9}}
	return execution, probe, request
}
func TestKnowledgeSnapshotGatewayUsesActualActionAuthorityAndOwnerCheck(t *testing.T) {
	execution, probe, request := runtimeSnapshotFixture()
	gateway := &bindableConnectorGateway{}
	if err := gateway.bind(integrationRuntimeConnectorGateway{runtimeID: "runtime-a", knowledgeDocuments: probe}); err != nil {
		t.Fatal(err)
	}
	value, err := CheckKnowledgeDocumentSnapshot(t.Context(), gateway, execution, request)
	if err != nil || value.ID != request.Snapshot.DocumentID || value.ContentVersion != 2 || value.IndexGeneration != 3 || probe.calls != 1 || probe.library != request.LibraryID || probe.snapshot != request.Snapshot || !probe.authority.Equal(knowledgesdk.Authority{Known: true, RuntimeID: "runtime-a", WorkspaceID: "workspace-a", UserID: "actual-reviewer", RoleKey: "support"}) {
		t.Fatal(value, err, probe)
	}
}
func TestKnowledgeSnapshotGatewayDoesNotDowngradeOrCallOwnerAfterWrite(t *testing.T) {
	execution, probe, request := runtimeSnapshotFixture()
	for _, gateway := range []ConnectorGateway{nil, unavailableRuntimeConnectorGateway{}, &bindableConnectorGateway{}} {
		if _, err := CheckKnowledgeDocumentSnapshot(t.Context(), gateway, execution, request); err == nil {
			t.Fatal("missing snapshot capability accepted")
		}
	}
	for name, change := range map[string]func(*integrationConnectorExecution, *KnowledgeDocumentSnapshotRequest){
		"no workspace": func(e *integrationConnectorExecution, _ *KnowledgeDocumentSnapshotRequest) { e.workspace.ID = "" },
		"no action": func(e *integrationConnectorExecution, _ *KnowledgeDocumentSnapshotRequest) {
			e.identity.ExecutionID = ""
		},
		"no user":           func(e *integrationConnectorExecution, _ *KnowledgeDocumentSnapshotRequest) { e.principal.UserID = "" },
		"unknown principal": func(e *integrationConnectorExecution, _ *KnowledgeDocumentSnapshotRequest) { e.principal.Known = false },
		"invalid version": func(_ *integrationConnectorExecution, r *KnowledgeDocumentSnapshotRequest) {
			r.Snapshot.ContentVersion = 0
		},
		"arbitrary library whitespace": func(_ *integrationConnectorExecution, r *KnowledgeDocumentSnapshotRequest) {
			r.LibraryID = " library-a"
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, p, r := runtimeSnapshotFixture()
			change(e, &r)
			_, err := (integrationRuntimeConnectorGateway{runtimeID: "runtime-a", knowledgeDocuments: p}).CheckKnowledgeDocumentSnapshot(t.Context(), e, r)
			if err == nil || p.calls != 0 {
				t.Fatal("invalid request reached owner", err, p.calls)
			}
		})
	}
	gateway := integrationRuntimeConnectorGateway{runtimeID: "runtime-a", knowledgeDocuments: &knowledgeDocumentServiceProbe{}}
	if _, err := gateway.CheckKnowledgeDocumentSnapshot(t.Context(), execution, request); err == nil {
		t.Fatal("plain metadata Get substituted for a snapshot check")
	}
	for _, phase := range []runtimeext.ExecutionPhase{runtimeext.ExecutionPhaseWriting, runtimeext.ExecutionPhaseCommitted, runtimeext.ExecutionPhaseRolledBack} {
		value, err := (integrationRuntimeConnectorGateway{runtimeID: "runtime-a", knowledgeDocuments: probe}).CheckKnowledgeDocumentSnapshot(t.Context(), snapshotPhaseExecution{integrationConnectorExecution: execution, phase: phase}, request)
		if err == nil || value.ID != "" || probe.calls != 0 {
			t.Fatal("snapshot check allowed outside prewrite", phase, value, err)
		}
	}
}
func TestKnowledgeSnapshotGatewayRejectsMismatchedOwnerMetadataAndPreservesDenial(t *testing.T) {
	for name, change := range map[string]func(*snapshotOwnerProbe){
		"foreign document":    func(p *snapshotOwnerProbe) { p.document.ID = "other" },
		"foreign library":     func(p *snapshotOwnerProbe) { p.document.LibraryID = "other" },
		"changed version":     func(p *snapshotOwnerProbe) { p.document.ContentVersion++ },
		"changed generation":  func(p *snapshotOwnerProbe) { p.document.IndexGeneration++ },
		"changed hash":        func(p *snapshotOwnerProbe) { p.document.SHA256 = strings.Repeat("b", 64) },
		"unknown index":       func(p *snapshotOwnerProbe) { p.document.IndexStatus = "FAILED" },
		"not ready":           func(p *snapshotOwnerProbe) { p.document.State = "deleting" },
		"not clean":           func(p *snapshotOwnerProbe) { p.document.SecurityStatus = "pending" },
		"reindexing":          func(p *snapshotOwnerProbe) { p.document.Reindexing = true },
		"unknown projection":  func(p *snapshotOwnerProbe) { p.document.ProjectionKind = "unknown" },
		"page without origin": func(p *snapshotOwnerProbe) { p.document.ProjectionKind = "page" },
	} {
		t.Run(name, func(t *testing.T) {
			e, p, r := runtimeSnapshotFixture()
			change(p)
			value, err := (integrationRuntimeConnectorGateway{runtimeID: "runtime-a", knowledgeDocuments: p}).CheckKnowledgeDocumentSnapshot(t.Context(), e, r)
			if err == nil || value.ID != "" {
				t.Fatal(value, err)
			}
		})
	}
	e, p, r := runtimeSnapshotFixture()
	denied := &knowledgesdk.Error{Class: "forbidden", Code: "current_library_denied"}
	p.err = denied
	value, err := (integrationRuntimeConnectorGateway{runtimeID: "runtime-a", knowledgeDocuments: p}).CheckKnowledgeDocumentSnapshot(t.Context(), e, r)
	if !errors.Is(err, denied) || value.ID != "" {
		t.Fatal("owner denial changed", value, err)
	}
}
