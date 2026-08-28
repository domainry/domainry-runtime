package automationseed

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

type automationExecutionSeedRepository struct {
	inserted   []automationmodel.AutomationRuleExecution
	workspaces []string
	err        error
}

func (repository *automationExecutionSeedRepository) InsertExecutionSeed(_ context.Context, workspaceID string, execution automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error) {
	if repository.err != nil {
		return automationmodel.AutomationRuleExecution{}, repository.err
	}
	repository.workspaces = append(repository.workspaces, workspaceID)
	repository.inserted = append(repository.inserted, execution)
	return execution, nil
}

func TestSyncExecutionSeeds(t *testing.T) {
	if err := SyncExecutionSeeds(t.Context(), nil, []automationmodel.AutomationRuleExecution{{}}); err != nil {
		t.Fatalf("nil repository error=%v", err)
	}
	repository := &automationExecutionSeedRepository{}
	if err := SyncExecutionSeeds(t.Context(), repository, []automationmodel.AutomationRuleExecution{{}}); err == nil || !strings.Contains(err.Error(), "seed 0: id is required") {
		t.Fatalf("missing id error=%v", err)
	}
	if err := SyncExecutionSeeds(t.Context(), repository, []automationmodel.AutomationRuleExecution{{ID: "execution-1"}}); err == nil || !strings.Contains(err.Error(), "rule_key, object_key, phase, operation, and status are required") {
		t.Fatalf("missing identity error=%v", err)
	}
	for _, seed := range []automationmodel.AutomationRuleExecution{
		{ID: "execution-1", RuleKey: "rule-1"},
		{ID: "execution-1", RuleKey: "rule-1", ObjectKey: "customer"},
		{ID: "execution-1", RuleKey: "rule-1", ObjectKey: "customer", Phase: "after"},
		{ID: "execution-1", RuleKey: "rule-1", ObjectKey: "customer", Phase: "after", Operation: "create"},
	} {
		if err := SyncExecutionSeeds(t.Context(), repository, []automationmodel.AutomationRuleExecution{seed}); err == nil || !strings.Contains(err.Error(), "required") {
			t.Fatalf("staged required field error for %+v = %v", seed, err)
		}
	}
	seeds := []automationmodel.AutomationRuleExecution{
		{ID: "execution-1", WorkspaceID: "workspace-1", RuleKey: "rule-1", ObjectKey: "customer", Phase: "after", Operation: "create", Status: "succeeded"},
		{ID: "execution-2", WorkspaceID: "workspace-2", RuleKey: "rule-2", ObjectKey: "order", Phase: "before", Operation: "update", Status: "failed"},
	}
	if err := SyncExecutionSeeds(t.Context(), repository, seeds); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repository.inserted, seeds) || !reflect.DeepEqual(repository.workspaces, []string{"workspace-1", "workspace-2"}) {
		t.Fatalf("inserted=%+v workspaces=%v", repository.inserted, repository.workspaces)
	}
	repository.err = errors.New("insert unavailable")
	if err := SyncExecutionSeeds(t.Context(), repository, seeds[:1]); err == nil || !strings.Contains(err.Error(), "sync automation execution seed execution-1: insert unavailable") {
		t.Fatalf("repository error=%v", err)
	}
}

func TestMergeRules(t *testing.T) {
	existing := []automationmodel.AutomationRuleSchema{{Key: "existing", Name: "Existing"}, {Key: " ", Name: "Legacy blank"}}
	generated := []automationmodel.AutomationRuleSchema{
		{Key: " existing ", Name: "Duplicate"},
		{Key: "generated", Name: "Generated"},
		{Key: "generated", Name: "Duplicate generated"},
		{Key: "", Name: "Blank"},
	}
	merged := MergeRules(existing, generated)
	if len(merged) != 3 || merged[0].Name != "Existing" || merged[1].Name != "Legacy blank" || merged[2].Name != "Generated" {
		t.Fatalf("merged=%+v", merged)
	}
	merged[0].Name = "Changed"
	if existing[0].Name != "Existing" {
		t.Fatalf("MergeRules mutated caller slice: %+v", existing)
	}
	if result := MergeRules(nil, nil); len(result) != 0 {
		t.Fatalf("empty merge=%#v", result)
	}
}
