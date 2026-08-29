package agent

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func scriptedAgentTaskStore(store *database.RuntimeStore, state *agentStateDBState) (*AgentTaskRunStore, func()) {
	db := sql.OpenDB(agentStateConnector{state: state})
	repository := NewAgentTaskRunStore(store)
	repository.db = db
	return repository, func() { _ = db.Close() }
}

func interactiveSchemaExecs(extra ...error) []error { return extra }
func taskSchemaExecs(extra ...error) []error        { return extra }

func validInteractiveRun(now time.Time) agentmodel.AgentInteractiveRun {
	return agentmodel.AgentInteractiveRun{ID: "interactive", WorkspaceID: "default", SessionID: "session", UserID: "user", RoleKey: "role", Surface: "business_workspace", Status: agentmodel.AgentInteractiveRunRunning, IdempotencyKey: "idem", CreatedAt: now, UpdatedAt: now, Revision: 1}
}

func TestAgentInteractiveRunStoreSchemaCreateAndReadFailureMatrix(t *testing.T) {
	base, now, wantErr := openAgentStateBaseStore(t), time.Unix(10, 0).UTC(), errors.New("interactive failure")
	mysqlBase := openAgentStateBaseStore(t)
	if err := mysqlBase.SetEngineForTesting("mysql"); err != nil {
		t.Fatal(err)
	}
	mysqlState := &agentStateDBState{execErrors: []error{nil}}
	mysqlRepo, mysqlClose := scriptedAgentTaskStore(mysqlBase, mysqlState)
	if err := migrateAgentInteractiveRunSchema(t.Context(), mysqlBase, mysqlRepo.db); err != nil || !strings.Contains(mysqlState.queries[0], "VARCHAR(255)") {
		t.Fatalf("mysql query=%v err=%v", mysqlState.queries, err)
	}
	mysqlClose()
	state := &agentStateDBState{execErrors: []error{nil}}
	repo, closeDB := scriptedAgentTaskStore(base, state)
	repo.store = base
	if err := migrateAgentInteractiveRunSchema(t.Context(), base, repo.db); err != nil {
		t.Fatal(err)
	}
	closeDB()
	state = &agentStateDBState{execErrors: []error{nil}}
	repo, closeDB = scriptedAgentTaskStore(base, state)
	repo.store = base
	// Driver-sensitive DDL is exercised by temporarily wrapping the already-open
	// RuntimeStore through its real MySQL coverage elsewhere; the sqlite branch is
	// asserted here through the emitted CREATE statement.
	if err := migrateAgentInteractiveRunSchema(t.Context(), base, repo.db); err != nil || len(state.queries) != 1 {
		t.Fatalf("schema queries=%v err=%v", state.queries, err)
	}
	closeDB()

	run := validInteractiveRun(now)
	for _, test := range []struct {
		name   string
		run    agentmodel.AgentInteractiveRun
		state  *agentStateDBState
		replay bool
	}{
		{"marshal", func() agentmodel.AgentInteractiveRun {
			v := run
			v.StructuredResult = map[string]any{"bad": make(chan int)}
			return v
		}(), &agentStateDBState{execErrors: interactiveSchemaExecs()}, false},
		{"insert", run, &agentStateDBState{execErrors: interactiveSchemaExecs(wantErr), querySteps: []agentStateQueryStep{{err: wantErr}}}, false},
		{"insert missing", run, &agentStateDBState{execErrors: interactiveSchemaExecs(wantErr), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}}}}, false},
		{"replay", run, &agentStateDBState{execErrors: interactiveSchemaExecs(wantErr), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{mustJSON(t, run)}}}}}, true},
	} {
		t.Run("create "+test.name, func(t *testing.T) {
			repository, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			got, replay, err := repository.CreateInteractiveRun(t.Context(), test.run)
			if test.replay {
				if err != nil || !replay || got.ID != run.ID {
					t.Fatalf("got=%#v replay=%v err=%v", got, replay, err)
				}
			} else if err == nil {
				t.Fatal("expected error")
			}
		})
	}
	for _, test := range []struct {
		name      string
		step      agentStateQueryStep
		found     bool
		wantError bool
	}{
		{"missing", agentStateQueryStep{columns: []string{"payload_json"}}, false, false},
		{"query", agentStateQueryStep{err: wantErr}, false, true},
		{"scan", agentStateQueryStep{columns: []string{"payload_json", "extra"}, rows: [][]driver.Value{{mustJSON(t, run), "extra"}}}, false, true},
		{"json", agentStateQueryStep{columns: []string{"payload_json"}, rows: [][]driver.Value{{[]byte("{")}}}, false, true},
		{"success", agentStateQueryStep{columns: []string{"payload_json"}, rows: [][]driver.Value{{mustJSON(t, run)}}}, true, false},
	} {
		t.Run("get "+test.name, func(t *testing.T) {
			repository, close := scriptedAgentTaskStore(base, &agentStateDBState{execErrors: interactiveSchemaExecs(), querySteps: []agentStateQueryStep{test.step}})
			defer close()
			got, ok, err := repository.GetInteractiveRun(t.Context(), "default", "interactive")
			if ok != test.found || test.wantError != (err != nil) || ok && got.ID != run.ID {
				t.Fatalf("got=%#v found=%v err=%v", got, ok, err)
			}
		})
	}
}

func TestAgentInteractiveRunStoreListAndSaveFailureMatrix(t *testing.T) {
	base, now, wantErr := openAgentStateBaseStore(t), time.Unix(10, 0).UTC(), errors.New("interactive failure")
	run := validInteractiveRun(now)
	payload := mustJSON(t, run)
	for _, test := range []struct {
		name      string
		filter    agentrepository.AgentInteractiveRunFilter
		step      agentStateQueryStep
		wantLen   int
		wantError bool
	}{
		{"query", agentrepository.AgentInteractiveRunFilter{}, agentStateQueryStep{err: wantErr}, 0, true},
		{"scan", agentrepository.AgentInteractiveRunFilter{}, agentStateQueryStep{columns: []string{"payload_json", "extra"}, rows: [][]driver.Value{{payload, "x"}}}, 0, true},
		{"json", agentrepository.AgentInteractiveRunFilter{}, agentStateQueryStep{columns: []string{"payload_json"}, rows: [][]driver.Value{{[]byte("{")}}}, 0, true},
		{"rows", agentrepository.AgentInteractiveRunFilter{}, agentStateQueryStep{columns: []string{"payload_json"}, nextErr: wantErr}, 0, true},
		{"statuses", agentrepository.AgentInteractiveRunFilter{Statuses: []agentmodel.AgentInteractiveRunStatus{agentmodel.AgentInteractiveRunRunning}, Limit: 1}, agentStateQueryStep{columns: []string{"payload_json"}, rows: [][]driver.Value{{payload}}}, 1, false},
		{"large limit", agentrepository.AgentInteractiveRunFilter{Limit: 101}, agentStateQueryStep{columns: []string{"payload_json"}}, 0, false},
	} {
		t.Run("list "+test.name, func(t *testing.T) {
			repository, close := scriptedAgentTaskStore(base, &agentStateDBState{execErrors: interactiveSchemaExecs(), querySteps: []agentStateQueryStep{test.step}})
			defer close()
			values, err := repository.ListInteractiveRuns(t.Context(), "default", "user", "role", test.filter)
			if len(values) != test.wantLen || test.wantError != (err != nil) {
				t.Fatalf("values=%#v err=%v", values, err)
			}
		})
	}
	for _, test := range []struct {
		name      string
		run       agentmodel.AgentInteractiveRun
		state     *agentStateDBState
		ok        bool
		wantError bool
	}{
		{"load error", run, &agentStateDBState{execErrors: interactiveSchemaExecs(), querySteps: []agentStateQueryStep{{err: wantErr}}}, false, true},
		{"missing", run, &agentStateDBState{execErrors: interactiveSchemaExecs(), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}}}}, false, false},
		{"revision", run, &agentStateDBState{execErrors: interactiveSchemaExecs(), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{mustJSON(t, func() agentmodel.AgentInteractiveRun { v := run; v.Revision = 2; return v }())}}}}}, false, false},
		{"marshal", func() agentmodel.AgentInteractiveRun {
			v := run
			v.Revision = 2
			v.StructuredResult = map[string]any{"bad": make(chan int)}
			return v
		}(), &agentStateDBState{execErrors: interactiveSchemaExecs(), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{payload}}}}}, false, true},
		{"update", func() agentmodel.AgentInteractiveRun { v := run; v.Revision = 2; return v }(), &agentStateDBState{execErrors: interactiveSchemaExecs(wantErr), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{payload}}}}}, false, true},
		{"rows error", func() agentmodel.AgentInteractiveRun { v := run; v.Revision = 2; return v }(), &agentStateDBState{execErrors: interactiveSchemaExecs(nil), resultErrors: []error{wantErr}, querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{payload}}}}}, false, true},
		{"miss", func() agentmodel.AgentInteractiveRun { v := run; v.Revision = 2; return v }(), &agentStateDBState{execErrors: interactiveSchemaExecs(nil), execRows: []int64{0}, querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{payload}}}}}, false, false},
		{"success", func() agentmodel.AgentInteractiveRun { v := run; v.Revision = 2; return v }(), &agentStateDBState{execErrors: interactiveSchemaExecs(nil), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{payload}}}}}, true, false},
	} {
		t.Run("save "+test.name, func(t *testing.T) {
			repository, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			ok, err := repository.SaveInteractiveRun(t.Context(), test.run, 1)
			if ok != test.ok || test.wantError != (err != nil) {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestAgentInteractiveRunStoreAtomicHandoffFailureMatrix(t *testing.T) {
	base, now, wantErr := openAgentStateBaseStore(t), time.Unix(10, 0).UTC(), errors.New("handoff failure")
	run := validInteractiveRun(now)
	task := agentmodel.AgentTaskRun{ID: "task", WorkspaceID: run.WorkspaceID, InteractiveRunID: run.ID, TaskKey: "task", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: "task-idem", CreatedAt: now, UpdatedAt: now, Revision: 1}
	allSchema := func(extra ...error) []error { return extra }
	stateFor := func(current agentmodel.AgentInteractiveRun, extra ...error) *agentStateDBState {
		return &agentStateDBState{execErrors: allSchema(extra...), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{mustJSON(t, current)}}}}}
	}
	replayed := run
	replayed.TaskRunID = "existing"
	completed := run
	completed.Status = agentmodel.AgentInteractiveRunCompleted
	revision := run
	revision.Revision = 2
	otherWorkspace := task
	otherWorkspace.WorkspaceID = "other"
	otherInteractive := task
	otherInteractive.InteractiveRunID = "other"
	badTask := task
	badTask.Input = map[string]any{"bad": make(chan int)}
	rowsError := stateFor(run, nil, nil)
	rowsError.resultErrors = []error{nil, nil, wantErr}
	rowsMiss := stateFor(run, nil, nil)
	rowsMiss.execRows = []int64{1, 1, 0}
	commitError := stateFor(run, nil, nil)
	commitError.commitErrors = []error{wantErr}
	for _, test := range []struct {
		name      string
		run       agentmodel.AgentInteractiveRun
		task      agentmodel.AgentTaskRun
		state     *agentStateDBState
		replay    bool
		wantError bool
	}{
		{"begin", run, task, &agentStateDBState{execErrors: allSchema(), beginErrors: []error{wantErr}}, false, true},
		{"query", run, task, &agentStateDBState{execErrors: allSchema(), querySteps: []agentStateQueryStep{{err: wantErr}}}, false, true},
		{"scan", run, task, &agentStateDBState{execErrors: allSchema(), querySteps: []agentStateQueryStep{{columns: []string{"payload_json", "extra"}, rows: [][]driver.Value{{mustJSON(t, run), "x"}}}}}, false, true},
		{"json", run, task, &agentStateDBState{execErrors: allSchema(), querySteps: []agentStateQueryStep{{columns: []string{"payload_json"}, rows: [][]driver.Value{{[]byte("{")}}}}}, false, true},
		{"replay", run, task, stateFor(replayed), true, false},
		{"revision", run, task, stateFor(revision), false, true},
		{"status", run, task, stateFor(completed), false, true},
		{"workspace", run, otherWorkspace, stateFor(run), false, true},
		{"interactive id", run, otherInteractive, stateFor(run), false, true},
		{"task marshal", run, badTask, stateFor(run), false, true},
		{"insert", run, task, stateFor(run, wantErr), false, true},
		{"update", run, task, stateFor(run, nil, wantErr), false, true},
		{"rows error", run, task, rowsError, false, true},
		{"rows miss", run, task, rowsMiss, false, true},
		{"commit", run, task, commitError, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			got, replay, err := repository.CommitInteractiveTaskHandoff(t.Context(), test.run, 1, test.task)
			if replay != test.replay || test.wantError != (err != nil) {
				t.Fatalf("got=%#v replay=%v err=%v", got, replay, err)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
