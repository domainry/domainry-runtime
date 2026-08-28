package agent

import (
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestAgentTaskRunStoreRequestCancelFailureMatrix(t *testing.T) {
	base := openAgentStateBaseStore(t)
	now := time.Unix(10, 0).UTC()
	wantErr := errors.New("cancel failure")
	pending := validTaskRun(now)
	running := pending
	running.Status = agentmodel.AgentTaskRunRunning
	retry := pending
	retry.Status = agentmodel.AgentTaskRunRetryScheduled
	terminal := pending
	terminal.Status = agentmodel.AgentTaskRunSucceeded
	requested := running
	requested.CancelRequestedAt = &now
	cancelState := func(run agentmodel.AgentTaskRun, updateErr error) *agentStateDBState {
		return &agentStateDBState{execErrors: taskSchemaExecs(updateErr), querySteps: []agentStateQueryStep{taskRunRow(t, run)}}
	}
	rowsErr := cancelState(pending, nil)
	rowsErr.resultErrors = []error{nil, nil, nil, nil, wantErr}
	rowsMiss := cancelState(pending, nil)
	rowsMiss.execRows = []int64{1, 1, 1, 1, 0}
	raced := func(latest agentStateQueryStep, trailingExecs ...error) *agentStateDBState {
		execs := taskSchemaExecs(nil)
		execs = append(execs, trailingExecs...)
		if len(trailingExecs) == 0 {
			execs = append(execs, taskSchemaExecs()...)
		}
		return &agentStateDBState{execErrors: execs, execRows: []int64{1, 1, 1, 1, 0}, querySteps: []agentStateQueryStep{taskRunRow(t, pending), latest}}
	}
	tests := []struct {
		name                string
		state               *agentStateDBState
		replayed, wantError bool
		wantStatus          agentmodel.AgentTaskRunStatus
	}{
		{"get schema", &agentStateDBState{execErrors: []error{wantErr}}, false, true, ""},
		{"get missing", taskState(agentStateQueryStep{columns: []string{"payload_json"}}), false, false, ""},
		{"get query", taskState(agentStateQueryStep{err: wantErr}), false, true, ""},
		{"terminal replay", taskState(taskRunRow(t, terminal)), true, false, terminal.Status},
		{"requested replay", taskState(taskRunRow(t, requested)), true, false, requested.Status},
		{"pending", cancelState(pending, nil), false, false, agentmodel.AgentTaskRunCancelled},
		{"retry", cancelState(retry, nil), false, false, agentmodel.AgentTaskRunCancelled},
		{"running", cancelState(running, nil), false, false, agentmodel.AgentTaskRunRunning},
		{"update", cancelState(pending, wantErr), false, true, ""},
		{"rows error", rowsErr, false, true, ""},
		{"race latest", raced(taskRunRow(t, terminal)), true, false, terminal.Status},
		{"race latest query", raced(agentStateQueryStep{err: wantErr}), true, true, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			got, replayed, err := repo.RequestCancel(t.Context(), "default", "run", "reason", now.Add(time.Second))
			if replayed != test.replayed || test.wantError != (err != nil) || test.wantStatus != "" && got.Status != test.wantStatus {
				t.Fatalf("got=%#v replayed=%v err=%v", got, replayed, err)
			}
		})
	}
}

func toolRun(now time.Time) agentmodel.AgentTaskRun {
	run := validTaskRun(now)
	run.Status = agentmodel.AgentTaskRunRunning
	run.Lease = agentmodel.AgentTaskLease{Owner: "worker", FencingToken: 2, ExpiresAt: now.Add(time.Minute)}
	return run
}

func toolRow(t *testing.T, run agentmodel.AgentTaskRun, status, owner string, token int64) agentStateQueryStep {
	return agentStateQueryStep{
		columns: []string{"payload_json", "status", "lease_owner", "fencing_token"},
		rows:    [][]driver.Value{{mustJSON(t, run), status, owner, token}},
	}
}

func toolState(step agentStateQueryStep, updateErr error) *agentStateDBState {
	return &agentStateDBState{execErrors: taskSchemaExecs(updateErr), querySteps: []agentStateQueryStep{step}}
}

func TestAgentTaskRunStoreBeginToolCallFailureMatrix(t *testing.T) {
	base := openAgentStateBaseStore(t)
	now := time.Unix(10, 0).UTC()
	wantErr := errors.New("begin tool failure")
	run := toolRun(now)
	row := toolRow(t, run, string(run.Status), "worker", 2)
	start := agentrepository.AgentToolCallStart{WorkspaceID: "default", TaskRunID: "run", Owner: "worker", FencingToken: 2, Tool: "object.get", MaxToolCalls: 2, CostUnits: 1, MaxCostUnits: 10}
	rowsErr := toolState(row, nil)
	rowsErr.resultErrors = []error{nil, nil, nil, nil, wantErr}
	rowsMiss := toolState(row, nil)
	rowsMiss.execRows = []int64{1, 1, 1, 1, 0}
	commitErr := toolState(row, nil)
	commitErr.commitErrors = []error{wantErr}
	used := run
	used.Evidence.ToolInvocations = []agentmodel.AgentTaskToolInvocationEvidence{{CostUnits: 10}}
	tests := []struct {
		name, code string
		start      agentrepository.AgentToolCallStart
		state      *agentStateDBState
		wantError  bool
	}{
		{"schema", "", start, &agentStateDBState{execErrors: []error{wantErr}}, true},
		{"begin", "", start, &agentStateDBState{execErrors: taskSchemaExecs(), beginErrors: []error{wantErr}}, true},
		{"query", "", start, toolState(agentStateQueryStep{err: wantErr}, nil), true},
		{"status fence", "agent.task.tool_fence_rejected", start, toolState(toolRow(t, run, string(agentmodel.AgentTaskRunPending), "worker", 2), nil), true},
		{"owner fence", "agent.task.tool_fence_rejected", start, toolState(toolRow(t, run, string(run.Status), "other", 2), nil), true},
		{"token fence", "agent.task.tool_fence_rejected", start, toolState(toolRow(t, run, string(run.Status), "worker", 3), nil), true},
		{"json", "", start, toolState(agentStateQueryStep{columns: row.columns, rows: [][]driver.Value{{[]byte("{"), string(run.Status), "worker", int64(2)}}}, nil), true},
		{"max disabled", "agent.task.tool_call_limit", withMaxCalls(start, 0), toolState(row, nil), true},
		{"max reached", "agent.task.tool_call_limit", withMaxCalls(start, 0), toolState(row, nil), true},
		{"cost disabled", "agent.task.cost_budget_exceeded", withCost(start, 0, 10), toolState(row, nil), true},
		{"budget disabled", "agent.task.cost_budget_exceeded", withCost(start, 1, 0), toolState(row, nil), true},
		{"budget exceeded", "agent.task.cost_budget_exceeded", withCost(start, 1, 10), toolState(toolRow(t, used, string(run.Status), "worker", 2), nil), true},
		{"update", "", start, toolState(row, wantErr), true},
		{"rows error", "", start, rowsErr, true},
		{"rows miss", "agent.task.tool_fence_rejected", start, rowsMiss, true},
		{"commit", "", start, commitErr, true},
		{"success", "", start, toolState(row, nil), false},
	}
	countReached := run
	countReached.ToolCallCount = 2
	tests[8].start = start
	tests[8].state = toolState(toolRow(t, countReached, string(run.Status), "worker", 2), nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			ref, count, err := repo.BeginAgentToolCall(t.Context(), test.start)
			if test.wantError != (err != nil) || test.code != "" && apperror.CodeOf(err) != test.code || !test.wantError && (ref == "" || count != 1) {
				t.Fatalf("ref=%q count=%d err=%v", ref, count, err)
			}
		})
	}
}

func withMaxCalls(start agentrepository.AgentToolCallStart, max int) agentrepository.AgentToolCallStart {
	start.MaxToolCalls = max
	return start
}

func withCost(start agentrepository.AgentToolCallStart, cost, max int) agentrepository.AgentToolCallStart {
	start.CostUnits, start.MaxCostUnits = cost, max
	return start
}

func TestAgentTaskRunStoreFinishToolCallFailureMatrix(t *testing.T) {
	base := openAgentStateBaseStore(t)
	now := time.Unix(10, 0).UTC()
	wantErr := errors.New("finish tool failure")
	run := toolRun(now)
	finishedAt := now.Add(time.Second)
	run.Evidence.ToolInvocations = []agentmodel.AgentTaskToolInvocationEvidence{{Ref: "other"}, {Ref: "call", StartedAt: now}}
	row := toolRow(t, run, string(run.Status), "worker", 2)
	finish := agentrepository.AgentToolCallFinish{WorkspaceID: "default", TaskRunID: "run", CallRef: "call", Owner: "worker", FencingToken: 2, Status: "succeeded", Evidence: map[string]any{"output_hash": " hash "}}
	rowsErr := toolState(row, nil)
	rowsErr.resultErrors = []error{nil, nil, nil, nil, wantErr}
	rowsMiss := toolState(row, nil)
	rowsMiss.execRows = []int64{1, 1, 1, 1, 0}
	commitErr := toolState(row, nil)
	commitErr.commitErrors = []error{wantErr}
	already := run
	already.Evidence.ToolInvocations[1].FinishedAt = &finishedAt
	tests := []struct {
		name, code string
		finish     agentrepository.AgentToolCallFinish
		state      *agentStateDBState
		wantError  bool
	}{
		{"schema", "", finish, &agentStateDBState{execErrors: []error{wantErr}}, true},
		{"begin", "", finish, &agentStateDBState{execErrors: taskSchemaExecs(), beginErrors: []error{wantErr}}, true},
		{"query", "", finish, toolState(agentStateQueryStep{err: wantErr}, nil), true},
		{"status fence", "agent.task.tool_fence_rejected", finish, toolState(toolRow(t, run, string(agentmodel.AgentTaskRunPending), "worker", 2), nil), true},
		{"owner fence", "agent.task.tool_fence_rejected", finish, toolState(toolRow(t, run, string(run.Status), "other", 2), nil), true},
		{"token fence", "agent.task.tool_fence_rejected", finish, toolState(toolRow(t, run, string(run.Status), "worker", 3), nil), true},
		{"json", "", finish, toolState(agentStateQueryStep{columns: row.columns, rows: [][]driver.Value{{[]byte("{"), string(run.Status), "worker", int64(2)}}}, nil), true},
		{"missing call", "agent.task.tool_call_not_found", withCallRef(finish, "missing"), toolState(row, nil), true},
		{"already finished", "", finish, toolState(toolRow(t, already, string(run.Status), "worker", 2), nil), false},
		{"update", "", finish, toolState(row, wantErr), true},
		{"rows error", "", finish, rowsErr, true},
		{"rows miss", "agent.task.tool_fence_rejected", finish, rowsMiss, true},
		{"commit", "", finish, commitErr, true},
		{"success", "", finish, toolState(row, nil), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			err := repo.FinishAgentToolCall(t.Context(), test.finish)
			if test.wantError != (err != nil) || test.code != "" && apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func withCallRef(finish agentrepository.AgentToolCallFinish, ref string) agentrepository.AgentToolCallFinish {
	finish.CallRef = ref
	return finish
}
