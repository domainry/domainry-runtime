package integrationtest

import (
	"sync"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func configureQuorum(required int) func(*definitionmodel.WorkflowSchema) {
	return func(workflow *definitionmodel.WorkflowSchema) {
		approval := workflow.Graph.Nodes[1].Contract.Approval
		approval.RequiredApprovals = required
		// Duplicate declarations must not create extra votes.
		approval.Resolvers[0].UserIDs = []string{"approver_a", "approver_b", "approver_c", "approver_a"}
	}
}

func quorumPrincipal(userID string, permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "approver", Permissions: permissions})
}

func TestWorkflowQuorumDecisions(t *testing.T) {
	for _, decision := range []string{"approved", "rejected", "returned"} {
		t.Run(decision, func(t *testing.T) {
			store, records := approvalModeTestRuntime(t, "quorum", configureQuorum(2))
			t.Cleanup(func() { _ = store.Close() })
			process, err := runWorkflowProcess(t, store, records, "approval_modes", nil, quorumPrincipal("initiator", "workflow.approval_modes.run"))
			if err != nil {
				t.Fatal(err)
			}
			repository := workflowProcessStore(store)
			tasks, err := repository.ListTasks(t.Context(), "workspace-primary", process.ID, "", "", 10)
			if err != nil || len(tasks) != 3 {
				t.Fatalf("tasks=%+v err=%v", tasks, err)
			}
			application := records.Applications().Workflows
			first := quorumPrincipal(tasks[0].AssigneeUserID, integrationWorkflowTaskDecisionPermissions()...)
			request := workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved", IdempotencyKey: "first-vote"}
			for range 2 {
				after, err := application.DecideTask(t.Context(), tasks[0].ID, request, first)
				if err != nil || after.Status != "waiting" {
					t.Fatalf("first vote/replay=%+v err=%v", after, err)
				}
			}
			second := quorumPrincipal(tasks[1].AssigneeUserID, integrationWorkflowTaskDecisionPermissions()...)
			request = workflowmodel.WorkflowTaskDecisionRequest{Decision: decision, IdempotencyKey: "second-vote"}
			for range 2 {
				after, err := application.DecideTask(t.Context(), tasks[1].ID, request, second)
				want := "rejected"
				if decision == "approved" {
					want = "completed"
				}
				if err != nil || after.Status != want {
					t.Fatalf("second vote/replay=%+v want=%s err=%v", after, want, err)
				}
			}
			remaining, found, err := repository.GetTask(t.Context(), "workspace-primary", tasks[2].ID)
			if err != nil || !found || remaining.Status != "cancelled" {
				t.Fatalf("remaining=%+v found=%v err=%v", remaining, found, err)
			}
			events, err := repository.ListEvents(t.Context(), "workspace-primary", process.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			completed := 0
			for _, event := range events {
				if event.Event == "approval_"+decision {
					completed++
				}
			}
			if completed != 1 {
				t.Fatalf("approval completion events=%d", completed)
			}
		})
	}
}

func TestWorkflowQuorumRejectsInsufficientResolvedAssignees(t *testing.T) {
	store, records := approvalModeTestRuntime(t, "quorum", configureQuorum(4))
	t.Cleanup(func() { _ = store.Close() })
	process, err := runWorkflowProcess(t, store, records, "approval_modes", nil, quorumPrincipal("initiator", "workflow.approval_modes.run"))
	if err != nil || process.Status != "configuration_error" || process.ErrorCode != "backend.workflow.approval_required_approvals_invalid" {
		t.Fatalf("invalid quorum process=%+v err=%v", process, err)
	}
	tasks, err := workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", "", "", "", 10)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("invalid quorum created tasks=%+v err=%v", tasks, err)
	}
}

func TestWorkflowQuorumConcurrentVotesAdvanceOnce(t *testing.T) {
	store, records := approvalModeTestRuntime(t, "quorum", configureQuorum(2))
	t.Cleanup(func() { _ = store.Close() })
	process, err := runWorkflowProcess(t, store, records, "approval_modes", nil, quorumPrincipal("initiator", "workflow.approval_modes.run"))
	if err != nil {
		t.Fatal(err)
	}
	repository := workflowProcessStore(store)
	tasks, err := repository.ListTasks(t.Context(), "workspace-primary", process.ID, "", "", 10)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	start := make(chan struct{})
	failures := make(chan error, 2)
	var group sync.WaitGroup
	for _, task := range tasks[:2] {
		group.Go(func() {
			<-start
			_, err := records.Applications().Workflows.DecideTask(t.Context(), task.ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved", IdempotencyKey: task.ID}, quorumPrincipal(task.AssigneeUserID, integrationWorkflowTaskDecisionPermissions()...))
			failures <- err
		})
	}
	close(start)
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	process, _, err = repository.GetProcess(t.Context(), "workspace-primary", process.ID)
	if err != nil || process.Status != "completed" {
		t.Fatalf("process=%+v err=%v", process, err)
	}
	tasks, err = repository.ListTasks(t.Context(), "workspace-primary", process.ID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	approved, cancelled := 0, 0
	for _, task := range tasks {
		if task.Status == "approved" {
			approved++
		}
		if task.Status == "cancelled" {
			cancelled++
		}
	}
	if approved != 2 || cancelled != 1 {
		t.Fatalf("approved=%d cancelled=%d", approved, cancelled)
	}
}
