package agent

import (
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

func taskPayloadStep(payload []byte) agentStateQueryStep {
	return agentStateQueryStep{columns: []string{"payload_json"}, rows: [][]driver.Value{{payload}}}
}

func taskQueryState(step agentStateQueryStep) *agentStateDBState {
	return &agentStateDBState{querySteps: []agentStateQueryStep{step}}
}

func TestAgentTaskRunStoreHeartbeatFailureMatrix(t *testing.T) {
	base := openAgentStateBaseStore(t)
	now := time.Unix(10, 0).UTC()
	wantErr := errors.New("heartbeat failure")
	run := validTaskRun(now)
	run.Status = agentmodel.AgentTaskRunRunning
	run.Lease = agentmodel.AgentTaskLease{Owner: "worker", FencingToken: 2, ExpiresAt: now.Add(time.Minute)}
	getState := func(step agentStateQueryStep, persist ...error) *agentStateDBState {
		return &agentStateDBState{execErrors: persist, querySteps: []agentStateQueryStep{step}}
	}
	rowsErr := &agentStateDBState{execErrors: []error{nil}, resultErrors: []error{wantErr}}
	rowsMiss := &agentStateDBState{execErrors: []error{nil}, execRows: []int64{0}}
	tests := []struct {
		name      string
		state     *agentStateDBState
		lost      bool
		wantError bool
	}{
		{"exec", &agentStateDBState{execErrors: []error{wantErr}}, false, true},
		{"rows error", rowsErr, false, true},
		{"lost", rowsMiss, true, false},
		{"get missing", getState(agentStateQueryStep{columns: []string{"payload_json"}}), false, false},
		{"get query", getState(agentStateQueryStep{err: wantErr}), false, true},
		{"get scan", getState(agentStateQueryStep{columns: []string{"payload_json", "extra"}, rows: [][]driver.Value{{mustJSON(t, run), "x"}}}), false, true},
		{"get json", getState(taskPayloadStep([]byte("{"))), false, true},
		{"persist", getState(taskRunRow(t, run), wantErr), false, true},
		{"success", getState(taskRunRow(t, run), nil), false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			result, err := repo.Heartbeat(t.Context(), "default", "run", "worker", 2, now, time.Minute)
			if result.Lost != test.lost || test.wantError != (err != nil) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func saveRunningState(t *testing.T, current agentmodel.AgentTaskRun, execErr error) *agentStateDBState {
	return &agentStateDBState{execErrors: []error{execErr}, querySteps: []agentStateQueryStep{taskRunRow(t, current)}}
}

func TestAgentTaskRunStoreSaveRunningFailureMatrix(t *testing.T) {
	base := openAgentStateBaseStore(t)
	now := time.Unix(10, 0).UTC()
	wantErr := errors.New("save running failure")
	current := validTaskRun(now)
	current.Status, current.Revision, current.ToolCallCount = agentmodel.AgentTaskRunRunning, 3, 1
	current.Evidence.ToolInvocationRefs = []string{"tool"}
	updated := current
	updated.Status, updated.Revision = agentmodel.AgentTaskRunSucceeded, 2
	highRevision := updated
	highRevision.Revision = 4
	bad := updated
	bad.Output = map[string]any{"bad": make(chan int)}
	rowsErr := saveRunningState(t, current, nil)
	rowsErr.resultErrors = []error{wantErr}
	rowsMiss := saveRunningState(t, current, nil)
	rowsMiss.execRows = []int64{0}
	commitErr := saveRunningState(t, current, nil)
	commitErr.commitErrors = []error{wantErr}
	tests := []struct {
		name, wantCode string
		run            agentmodel.AgentTaskRun
		state          *agentStateDBState
		wantError      bool
	}{
		{"begin", "", updated, &agentStateDBState{beginErrors: []error{wantErr}}, true},
		{"missing", "agent.task.terminal_fence_rejected", updated, taskQueryState(agentStateQueryStep{columns: []string{"payload_json"}}), true},
		{"query", "", updated, taskQueryState(agentStateQueryStep{err: wantErr}), true},
		{"scan", "", updated, taskQueryState(agentStateQueryStep{columns: []string{"payload_json", "extra"}, rows: [][]driver.Value{{mustJSON(t, current), "x"}}}), true},
		{"json", "", updated, taskQueryState(taskPayloadStep([]byte("{"))), true},
		{"marshal", "", bad, saveRunningState(t, current, nil), true},
		{"update", "", updated, saveRunningState(t, current, wantErr), true},
		{"rows error", "", updated, rowsErr, true},
		{"fence", "agent.task.terminal_fence_rejected", updated, rowsMiss, true},
		{"commit", "", updated, commitErr, true},
		{"high revision", "", highRevision, saveRunningState(t, current, nil), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			err := repo.SaveRunning(t.Context(), test.run, "worker", 2)
			if test.wantError != (err != nil) || test.wantCode != "" && apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestAgentTaskRunStoreApprovalOverrideAndOperationFailureMatrix(t *testing.T) {
	base := openAgentStateBaseStore(t)
	now := time.Unix(10, 0).UTC()
	wantErr := errors.New("transition failure")
	waiting := validTaskRun(now)
	waiting.Status, waiting.Revision = agentmodel.AgentTaskRunWaitingApproval, 3
	waiting.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal"}
	resolved := waiting
	resolved.Status, resolved.Revision = agentmodel.AgentTaskRunSucceeded, 4
	approvalConflicts := []struct {
		current, updated agentmodel.AgentTaskRun
	}{
		{withTaskApproval(waiting, nil), resolved},
		{waiting, withTaskApproval(resolved, nil)},
		{waiting, withTaskApproval(resolved, &agentmodel.AgentTaskApproval{ProposalID: "other"})},
	}
	for _, conflict := range approvalConflicts {
		repo, close := scriptedAgentTaskStore(base, taskQueryState(taskRunRow(t, conflict.current)))
		if err := repo.SaveWaitingApproval(t.Context(), conflict.updated, waiting.Revision); apperror.CodeOf(err) != "agent.task.approval_state_conflict" {
			t.Fatalf("approval conflict err=%v", err)
		}
		close()
	}
	type saveCall func(*AgentTaskRunStore, agentmodel.AgentTaskRun) error
	suites := []struct {
		name, conflict string
		base, updated  agentmodel.AgentTaskRun
		call           saveCall
	}{
		{"approval", "agent.task.approval_state_conflict", waiting, resolved, func(r *AgentTaskRunStore, v agentmodel.AgentTaskRun) error {
			return r.SaveWaitingApproval(t.Context(), v, 3)
		}},
		{"override", "agent.task.override_state_conflict", withTaskRevision(resolved, 3), resolved, func(r *AgentTaskRunStore, v agentmodel.AgentTaskRun) error {
			return r.SaveTerminalOverride(t.Context(), v, 3)
		}},
		{"operation", "agent.task.operation_state_conflict", withTaskRevision(resolved, 3), resolved, func(r *AgentTaskRunStore, v agentmodel.AgentTaskRun) error {
			return r.SaveOperationalTransition(t.Context(), v, agentmodel.AgentTaskRunSucceeded, 3)
		}},
	}
	for _, suite := range suites {
		t.Run(suite.name, func(t *testing.T) {
			jsonCode := ""
			if suite.name == "operation" {
				jsonCode = suite.conflict
			}
			if suite.name == "override" {
				repo, close := scriptedAgentTaskStore(base, &agentStateDBState{})
				if code := apperror.CodeOf(repo.SaveTerminalOverride(t.Context(), waiting, 3)); code != "agent.task.override_terminal_required" {
					t.Fatalf("code=%s", code)
				}
				close()
			}
			bad := suite.updated
			bad.Output = map[string]any{"bad": make(chan int)}
			row := taskRunRow(t, suite.base)
			rowsErr := &agentStateDBState{execErrors: []error{nil}, resultErrors: []error{wantErr}, querySteps: []agentStateQueryStep{row}}
			rowsMiss := &agentStateDBState{execErrors: []error{nil}, execRows: []int64{0}, querySteps: []agentStateQueryStep{row}}
			commitErr := &agentStateDBState{execErrors: []error{nil}, commitErrors: []error{wantErr}, querySteps: []agentStateQueryStep{row}}
			tests := []struct {
				name, code string
				run        agentmodel.AgentTaskRun
				state      *agentStateDBState
			}{
				{"begin", "", suite.updated, &agentStateDBState{beginErrors: []error{wantErr}}},
				{"missing", suite.conflict, suite.updated, taskQueryState(agentStateQueryStep{columns: []string{"payload_json"}})},
				{"query", "", suite.updated, taskQueryState(agentStateQueryStep{err: wantErr})},
				{"json", jsonCode, suite.updated, taskQueryState(taskPayloadStep([]byte("{")))},
				{"revision", suite.conflict, suite.updated, taskQueryState(taskRunRow(t, withTaskRevision(suite.base, 9)))},
				{"marshal", "", bad, taskQueryState(row)},
				{"update", "", suite.updated, &agentStateDBState{execErrors: []error{wantErr}, querySteps: []agentStateQueryStep{row}}},
				{"rows error", "", suite.updated, rowsErr},
				{"rows miss", suite.conflict, suite.updated, rowsMiss},
				{"commit", "", suite.updated, commitErr},
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					repo, close := scriptedAgentTaskStore(base, test.state)
					defer close()
					err := suite.call(repo, test.run)
					if err == nil || test.code != "" && apperror.CodeOf(err) != test.code {
						t.Fatalf("err=%v", err)
					}
				})
			}
		})
	}
}

func withTaskRevision(run agentmodel.AgentTaskRun, revision int64) agentmodel.AgentTaskRun {
	run.Revision = revision
	return run
}

func withTaskApproval(run agentmodel.AgentTaskRun, approval *agentmodel.AgentTaskApproval) agentmodel.AgentTaskRun {
	run.Approval = approval
	return run
}
