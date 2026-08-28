package globalcapabilityseed

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type globalCapabilityAuditRepository struct {
	events      []auditmodel.AuditEvent
	listErr     error
	insertErrAt int
	workspace   string
}

func (repository *globalCapabilityAuditRepository) ListAuditEvents(ctx context.Context, _ string, _ auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	repository.workspace = requestcontext.WorkspaceID(ctx)
	return append([]auditmodel.AuditEvent(nil), repository.events...), repository.listErr
}

func (repository *globalCapabilityAuditRepository) InsertAuditEvent(_ context.Context, _ string, event auditmodel.AuditEvent) error {
	if repository.insertErrAt > 0 && len(repository.events)+1 == repository.insertErrAt {
		return errors.New("audit insert unavailable")
	}
	repository.events = append(repository.events, event)
	return nil
}

type globalCapabilityWorkflowRepository struct {
	executions  []workflowmodel.WorkflowExecution
	listErr     error
	insertErrAt int
	workspace   string
}

func (repository *globalCapabilityWorkflowRepository) ListExecutions(ctx context.Context, _ string, _ int) ([]workflowmodel.WorkflowExecution, error) {
	repository.workspace = requestcontext.WorkspaceID(ctx)
	return append([]workflowmodel.WorkflowExecution(nil), repository.executions...), repository.listErr
}

func (repository *globalCapabilityWorkflowRepository) InsertExecution(_ context.Context, _ string, execution workflowmodel.WorkflowExecution) error {
	if repository.insertErrAt > 0 && len(repository.executions)+1 == repository.insertErrAt {
		return errors.New("workflow insert unavailable")
	}
	repository.executions = append(repository.executions, execution)
	return nil
}

func TestGeneratedGlobalCapabilitySchemaAndMerges(t *testing.T) {
	existingDictionary := metadatamodel.DictionarySchema{Key: "platform_operation_status", Name: "Custom status"}
	existingWorkflow := definitionmodel.WorkflowSchema{Key: "platform.audit_retention_check", Name: "Custom retention"}
	manifest := WithGeneratedSchema(manifestmodel.ManifestSchema{
		Objects:      []definitionmodel.ObjectSchema{{Key: "customer"}, {Key: "record_timer", Name: "Custom timer"}},
		Dictionaries: []metadatamodel.DictionarySchema{existingDictionary},
		Workflows:    []definitionmodel.WorkflowSchema{existingWorkflow},
	})
	if len(manifest.Dictionaries) != 3 || manifest.Dictionaries[0].Name != "Custom status" {
		t.Fatalf("dictionaries=%+v", manifest.Dictionaries)
	}
	if len(manifest.Workflows) != 2 || manifest.Workflows[0].Name != "Custom retention" {
		t.Fatalf("workflows=%+v", manifest.Workflows)
	}
	if len(manifest.Objects) != 6 || manifest.Objects[0].Key != "customer" || manifest.Objects[5].Key != "record_timer" || manifest.Objects[5].Name != "Record Timer" {
		t.Fatalf("canonical system objects=%+v", manifest.Objects)
	}
	if len(manifest.Dictionaries[1].Items) == 0 || manifest.Workflows[1].Graph == nil {
		t.Fatalf("generated definitions incomplete: dictionaries=%+v workflows=%+v", manifest.Dictionaries, manifest.Workflows)
	}
	existingDictionaries := []metadatamodel.DictionarySchema{{Key: " ", Name: "Blank existing"}, {Key: "custom", Name: "Custom"}}
	dictionaries := MergeDictionaries(
		existingDictionaries,
		[]metadatamodel.DictionarySchema{{Key: ""}, {Key: " custom ", Name: "Duplicate"}, {Key: "new", Name: "New"}, {Key: "new", Name: "Duplicate new"}},
	)
	if len(dictionaries) != 3 || dictionaries[2].Name != "New" {
		t.Fatalf("merged dictionaries=%+v", dictionaries)
	}
	existingWorkflows := []definitionmodel.WorkflowSchema{{Key: " ", Name: "Blank existing"}, {Key: "custom", Name: "Custom"}}
	workflows := MergeWorkflows(
		existingWorkflows,
		[]definitionmodel.WorkflowSchema{{Key: ""}, {Key: " custom ", Name: "Duplicate"}, {Key: "new", Name: "New"}, {Key: "new", Name: "Duplicate new"}},
	)
	if len(workflows) != 3 || workflows[2].Name != "New" {
		t.Fatalf("merged workflows=%+v", workflows)
	}
	dictionaries[0].Name, workflows[0].Name = "Changed", "Changed"
	if existingDictionaries[0].Name != "Blank existing" || existingWorkflows[0].Name != "Blank existing" {
		t.Fatal("merge mutated caller slice")
	}
}

func TestSyncRows(t *testing.T) {
	auditRepository := &globalCapabilityAuditRepository{}
	workflowRepository := &globalCapabilityWorkflowRepository{}
	if err := SyncRows(t.Context(), nil, workflowRepository); err != nil {
		t.Fatalf("nil audit error=%v", err)
	}
	if err := SyncRows(t.Context(), auditRepository, nil); err != nil {
		t.Fatalf("nil workflow error=%v", err)
	}

	auditRepository.listErr = errors.New("audit list unavailable")
	if err := SyncRows(t.Context(), auditRepository, workflowRepository); !errors.Is(err, auditRepository.listErr) {
		t.Fatalf("audit list error=%v", err)
	}
	auditRepository.listErr = nil
	auditRepository.insertErrAt = 1
	if err := SyncRows(t.Context(), auditRepository, workflowRepository); err == nil || err.Error() != "audit insert unavailable" {
		t.Fatalf("first audit insert error=%v", err)
	}
	auditRepository = &globalCapabilityAuditRepository{events: []auditmodel.AuditEvent{{ID: "existing"}}}
	workflowRepository.listErr = errors.New("workflow list unavailable")
	if err := SyncRows(t.Context(), auditRepository, workflowRepository); !errors.Is(err, workflowRepository.listErr) {
		t.Fatalf("workflow list error=%v", err)
	}
	workflowRepository.listErr = nil
	workflowRepository.insertErrAt = 1
	if err := SyncRows(t.Context(), auditRepository, workflowRepository); err == nil || err.Error() != "workflow insert unavailable" {
		t.Fatalf("first workflow insert error=%v", err)
	}
	workflowRepository.insertErrAt = 2
	if err := SyncRows(t.Context(), auditRepository, workflowRepository); err == nil || err.Error() != "workflow insert unavailable" {
		t.Fatalf("second workflow insert error=%v", err)
	}

	auditRepository = &globalCapabilityAuditRepository{}
	workflowRepository = &globalCapabilityWorkflowRepository{}
	if err := SyncRows(t.Context(), auditRepository, workflowRepository); err != nil {
		t.Fatal(err)
	}
	if len(auditRepository.events) != 1 || len(workflowRepository.executions) != 2 {
		t.Fatalf("seed counts audit=%d workflow=%d", len(auditRepository.events), len(workflowRepository.executions))
	}
	if auditRepository.workspace != principalmodel.InstallationWorkspaceID || workflowRepository.workspace != principalmodel.InstallationWorkspaceID {
		t.Fatalf("bootstrap workspace audit=%q workflow=%q", auditRepository.workspace, workflowRepository.workspace)
	}
	if auditRepository.events[0].ID == "" || workflowRepository.executions[0].ID == "" {
		t.Fatalf("seed identities audit=%+v workflow=%+v", auditRepository.events, workflowRepository.executions)
	}
	if err := SyncRows(t.Context(), auditRepository, workflowRepository); err != nil || len(auditRepository.events) != 1 || len(workflowRepository.executions) != 2 {
		t.Fatalf("idempotent sync error=%v audit=%d workflow=%d", err, len(auditRepository.events), len(workflowRepository.executions))
	}
}
