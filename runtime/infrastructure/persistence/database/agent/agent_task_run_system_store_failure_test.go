package agent

import (
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestAgentTaskRunSystemStoreFailureMatrix(t *testing.T) {
	base := openAgentStateBaseStore(t)
	now := time.Unix(10, 0).UTC()
	wantErr := errors.New("system worker store failure")
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test")
	run := validTaskRun(now)
	if _, err := NewAgentTaskRunStore(base).ListAgentTaskRunsForWorker(t.Context(), principalmodel.SystemScope{}, agentrepository.AgentTaskRunFilter{}); !errors.Is(err, principalmodel.ErrSystemScopeRequired) {
		t.Fatalf("list scope err=%v", err)
	}
	if _, _, err := NewAgentTaskRunStore(base).ClaimNextAgentTaskRunForWorker(t.Context(), principalmodel.SystemScope{}, "worker", now, time.Minute); !errors.Is(err, principalmodel.ErrSystemScopeRequired) {
		t.Fatalf("claim scope err=%v", err)
	}
	listTests := []struct {
		name      string
		filter    agentrepository.AgentTaskRunFilter
		state     *agentStateDBState
		wantError bool
	}{
		{"schema", agentrepository.AgentTaskRunFilter{}, &agentStateDBState{execErrors: []error{wantErr}}, true},
		{"query", agentrepository.AgentTaskRunFilter{}, taskState(agentStateQueryStep{err: wantErr}), true},
		{"scan", agentrepository.AgentTaskRunFilter{}, taskState(agentStateQueryStep{columns: []string{"payload_json", "extra"}, rows: [][]driver.Value{{mustJSON(t, run), "x"}}}), true},
		{"default limit", agentrepository.AgentTaskRunFilter{}, taskState(taskRunRow(t, run)), false},
		{"large limit", agentrepository.AgentTaskRunFilter{Limit: 501}, taskState(agentStateQueryStep{columns: []string{"payload_json"}}), false},
	}
	for _, test := range listTests {
		t.Run("list "+test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			_, err := repo.ListAgentTaskRunsForWorker(t.Context(), scope, test.filter)
			if test.wantError != (err != nil) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	claimTests := []struct {
		name      string
		state     *agentStateDBState
		wantError bool
	}{
		{"schema", &agentStateDBState{execErrors: []error{wantErr}}, true},
		{"missing", taskState(agentStateQueryStep{columns: []string{"workspace_id"}}), false},
		{"query", taskState(agentStateQueryStep{err: wantErr}), true},
	}
	for _, test := range claimTests {
		t.Run("claim "+test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			_, found, err := repo.ClaimNextAgentTaskRunForWorker(t.Context(), scope, "worker", now, time.Minute)
			if found || test.wantError != (err != nil) {
				t.Fatalf("found=%v err=%v", found, err)
			}
		})
	}
}
