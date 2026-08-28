package workflow

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func workflowDefinitionFailureStore(t *testing.T, state *workflowSQLState) WorkflowDefinitionStore {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	db := openWorkflowScriptedDB(state)
	t.Cleanup(func() {
		_ = db.Close()
		_ = store.Close()
	})
	repository := NewWorkflowDefinitionStore(store)
	repository.db = db
	return repository
}

func TestWorkflowDefinitionStoreTransactionFailures(t *testing.T) {
	definition := workflowmodel.WorkflowDefinition{ID: "definition"}
	version := workflowmodel.WorkflowDefinitionVersion{ID: "version", DefinitionID: definition.ID, Status: workflowmodel.WorkflowVersionDraft}
	tests := []struct {
		name  string
		state workflowSQLState
		run   func(WorkflowDefinitionStore) error
	}{
		{"insert begin", workflowSQLState{beginErr: errWorkflowSQL}, func(store WorkflowDefinitionStore) error {
			return store.InsertDefinition(t.Context(), definition, version)
		}},
		{"insert identity", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			return store.InsertDefinition(t.Context(), definition, version)
		}},
		{"insert version", workflowSQLState{execSteps: []workflowSQLExecStep{{rows: 1}, {err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			return store.InsertDefinition(t.Context(), definition, version)
		}},
		{"insert commit", workflowSQLState{commitErr: errWorkflowSQL}, func(store WorkflowDefinitionStore) error {
			return store.InsertDefinition(t.Context(), definition, version)
		}},
		{"draft begin", workflowSQLState{beginErr: errWorkflowSQL}, func(store WorkflowDefinitionStore) error {
			_, err := store.InsertDraftVersion(t.Context(), definition.ID, version)
			return err
		}},
		{"draft update", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.InsertDraftVersion(t.Context(), definition.ID, version)
			return err
		}},
		{"draft rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.InsertDraftVersion(t.Context(), definition.ID, version)
			return err
		}},
		{"draft insert", workflowSQLState{execSteps: []workflowSQLExecStep{{rows: 1}, {err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.InsertDraftVersion(t.Context(), definition.ID, version)
			return err
		}},
		{"draft commit", workflowSQLState{commitErr: errWorkflowSQL}, func(store WorkflowDefinitionStore) error {
			_, err := store.InsertDraftVersion(t.Context(), definition.ID, version)
			return err
		}},
		{"delete begin", workflowSQLState{beginErr: errWorkflowSQL}, func(store WorkflowDefinitionStore) error {
			_, err := store.DeleteDraft(t.Context(), definition.ID, version.ID)
			return err
		}},
		{"delete row", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.DeleteDraft(t.Context(), definition.ID, version.ID)
			return err
		}},
		{"delete rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.DeleteDraft(t.Context(), definition.ID, version.ID)
			return err
		}},
		{"delete identity", workflowSQLState{execSteps: []workflowSQLExecStep{{rows: 1}, {err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.DeleteDraft(t.Context(), definition.ID, version.ID)
			return err
		}},
		{"delete commit", workflowSQLState{commitErr: errWorkflowSQL}, func(store WorkflowDefinitionStore) error {
			_, err := store.DeleteDraft(t.Context(), definition.ID, version.ID)
			return err
		}},
		{"publish begin", workflowSQLState{beginErr: errWorkflowSQL}, func(store WorkflowDefinitionStore) error {
			_, err := store.PublishDraft(t.Context(), definition, version, "key")
			return err
		}},
		{"publish version", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.PublishDraft(t.Context(), definition, version, "key")
			return err
		}},
		{"publish rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.PublishDraft(t.Context(), definition, version, "key")
			return err
		}},
		{"publish identity", workflowSQLState{execSteps: []workflowSQLExecStep{{rows: 1}, {err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.PublishDraft(t.Context(), definition, version, "key")
			return err
		}},
		{"publish commit", workflowSQLState{commitErr: errWorkflowSQL}, func(store WorkflowDefinitionStore) error {
			_, err := store.PublishDraft(t.Context(), definition, version, "key")
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(workflowDefinitionFailureStore(t, &test.state)); !errors.Is(err, errWorkflowSQL) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestWorkflowDefinitionStoreDirectSQLFailures(t *testing.T) {
	version := workflowmodel.WorkflowDefinitionVersion{ID: "version"}
	tests := []struct {
		name  string
		state workflowSQLState
		run   func(WorkflowDefinitionStore) error
	}{
		{"get definition", workflowSQLState{querySteps: []workflowSQLQueryStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, _, err := store.GetDefinitionByKey(t.Context(), "key")
			return err
		}},
		{"list definitions query", workflowSQLState{querySteps: []workflowSQLQueryStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error { _, err := store.ListDefinitions(t.Context()); return err }},
		{"list definitions scan", workflowSQLState{querySteps: []workflowSQLQueryStep{{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.ListDefinitions(t.Context())
			return errors.Join(err, errWorkflowSQL)
		}},
		{"list definitions rows", workflowSQLState{querySteps: []workflowSQLQueryStep{{nextErr: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error { _, err := store.ListDefinitions(t.Context()); return err }},
		{"get version", workflowSQLState{querySteps: []workflowSQLQueryStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, _, err := store.GetVersion(t.Context(), version.ID)
			return err
		}},
		{"list versions query", workflowSQLState{querySteps: []workflowSQLQueryStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.ListVersions(t.Context(), "definition")
			return err
		}},
		{"list versions scan", workflowSQLState{querySteps: []workflowSQLQueryStep{{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.ListVersions(t.Context(), "definition")
			return errors.Join(err, errWorkflowSQL)
		}},
		{"list versions rows", workflowSQLState{querySteps: []workflowSQLQueryStep{{nextErr: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.ListVersions(t.Context(), "definition")
			return err
		}},
		{"update draft exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.UpdateDraft(t.Context(), version, 1)
			return err
		}},
		{"update draft rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.UpdateDraft(t.Context(), version, 1)
			return err
		}},
		{"archive exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.ArchiveVersion(t.Context(), "definition", version.ID, "now")
			return err
		}},
		{"archive rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.ArchiveVersion(t.Context(), "definition", version.ID, "now")
			return err
		}},
		{"enable exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.SetDefinitionEnabled(t.Context(), "definition", true, "now")
			return err
		}},
		{"enable rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowDefinitionStore) error {
			_, err := store.SetDefinitionEnabled(t.Context(), "definition", true, "now")
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(workflowDefinitionFailureStore(t, &test.state)); !errors.Is(err, errWorkflowSQL) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestWorkflowDefinitionStoreNoopMutations(t *testing.T) {
	store := workflowDefinitionFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{rows: 0}}})
	if updated, err := store.InsertDraftVersion(context.Background(), "definition", workflowmodel.WorkflowDefinitionVersion{}); err != nil || updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
}

func TestWorkflowDefinitionStoreMissingDefinition(t *testing.T) {
	store := workflowDefinitionFailureStore(t, &workflowSQLState{})
	if value, found, err := store.GetDefinitionByKey(t.Context(), "missing"); err != nil || found || value.ID != "" {
		t.Fatalf("value=%#v found=%v err=%v", value, found, err)
	}
}
