package integrationtest

import (
	"context"
	"encoding/json"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

const routeWithdrawKey = "expense_request.withdraw"

type routeWithdrawHandler struct{ abort bool }

func (*routeWithdrawHandler) Descriptor() runtimeext.HandlerDescriptor {
	return runtimeext.HandlerDescriptor{ActionKey: routeWithdrawKey, InputType: routeTypeBase + "WithdrawInput", OutputType: routeTypeBase + "WithdrawOutput", HandlerRevision: "withdraw-v1", ObjectCapabilities: []runtimeext.ActionObjectCapability{{ObjectKey: "expense_request", Operations: []string{"update"}}}, Workflows: []runtimeext.WorkflowGrant{{Key: routeWorkflowKey, Operations: []string{runtimeext.WorkflowWithdrawOperation}}}}
}

func (h *routeWithdrawHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		ProcessID string `json:"process_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	receipt, err := runtimeext.StageWorkflowWithdrawal(ctx, execution, runtimeext.WorkflowWithdrawal{WorkflowKey: routeWorkflowKey, ObjectKey: "expense_request", RecordID: execution.Identity().RecordID, ProcessID: input.ProcessID})
	if err != nil {
		return nil, err
	}
	_, err = execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{Operation: runtimeext.MutationUpdate, ObjectKey: "expense_request", RecordID: execution.Identity().RecordID, Fields: map[string]any{"state": "withdrawn", "withdrawal_receipt": receipt.CommandID}})
	if err != nil {
		return nil, err
	}
	if h.abort {
		return nil, &runtimeext.BusinessError{Code: "withdraw.aborted", Message: "rollback after staging business and Workflow changes"}
	}
	return json.Marshal(map[string]any{"process_id": receipt.ProcessID, "command_id": receipt.CommandID})
}

func routeWithdrawalManifest(t *testing.T, directory string) string {
	t.Helper()
	path := routeWorkflowManifest(t, directory, routeDefaultContract())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	action := map[string]any{"key": routeWithdrawKey, "object_key": "expense_request", "label": "Withdraw expense", "kind": "record_operation", "preconditions": []any{}, "audit_event": "expense_withdrawn", "input_type": routeTypeBase + "WithdrawInput", "output_type": routeTypeBase + "WithdrawOutput", "payload_fields": []any{map[string]any{"key": "process_id", "type": "text", "required": true}}}
	manifest["actions"] = append(manifest["actions"].([]any), action)
	object := manifest["objects"].([]any)[0].(map[string]any)
	object["fields"] = append(object["fields"].([]any), map[string]any{"key": "withdrawal_receipt", "name": "Withdrawal receipt", "type": "text"})
	for _, rawRole := range manifest["roles"].([]any) {
		role := rawRole.(map[string]any)
		role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": routeWithdrawKey, "data_scope": "all"})
	}
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWorkflowWithdrawalActionCommitsReceiptAndBusinessReleaseOrRollsBack(t *testing.T) {
	for _, abort := range []bool{true, false} {
		name := "commit"
		if abort {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			temp := t.TempDir()
			runtime := newRouteWorkflowRuntime(t, config.Config{AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"), ManifestPath: routeWithdrawalManifest(t, temp), UploadDir: filepath.Join(temp, "uploads")}, routeDefaultContract(), routeStandardUsers(), &routeWithdrawHandler{abort: abort})
			handler := runtime.Routes()
			submitted := routeSubmit(t, handler, map[string]any{"title": "Laptop", "steps": []any{map[string]any{"step_key": "manager", "assignees": []any{"route_user_a"}}}}, nil)
			if submitted.status != http.StatusOK {
				t.Fatalf("submit=%s", submitted.raw)
			}
			output := submitted.body["output"].(map[string]any)
			processID, recordID := output["process_id"].(string), output["record_id"].(string)
			task := routeAwaitOpenTask(t, handler, "route_user_a")
			path := "/records/expense_request/items/" + recordID + "/actions/" + routeWithdrawKey
			payload := map[string]any{"data": map[string]any{"process_id": processID}}
			denied := routeRequest(t, handler, "route_user_b", routeApproverRole, http.MethodPost, path, payload, nil)
			if denied.status != http.StatusForbidden {
				t.Fatalf("non-initiator withdrawal=%s", denied.raw)
			}
			native := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodPost, "/workflow/processes/"+processID+"/withdraw", nil, nil)
			if native.status != http.StatusConflict {
				t.Fatalf("native route bypassed business release=%s", native.raw)
			}
			headers := map[string]string{"Idempotency-Key": "withdraw-once"}
			withdrawn := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodPost, path, payload, headers)
			detail := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes/"+processID, nil, nil)
			record := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/records/expense_request/items/"+recordID, nil, nil)
			if detail.status != http.StatusOK || record.status != http.StatusOK {
				t.Fatalf("detail=%s record=%s", detail.raw, record.raw)
			}
			process := detail.body["process"].(map[string]any)
			fields := record.body["data"].(map[string]any)
			if abort {
				if withdrawn.status < 400 || process["status"] != "waiting" || fields["state"] != "submitted" || len(routeOpenTasks(t, handler, "route_user_a")) != 1 {
					t.Fatalf("rollback withdrawal=%s process=%s record=%s", withdrawn.raw, detail.raw, record.raw)
				}
				return
			}
			if withdrawn.status != http.StatusOK || process["status"] != "cancelled" || fields["state"] != "withdrawn" || fields["withdrawal_receipt"] == "" || len(routeOpenTasks(t, handler, "route_user_a")) != 0 {
				t.Fatalf("commit withdrawal=%s process=%s record=%s", withdrawn.raw, detail.raw, record.raw)
			}
			replay := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodPost, path, payload, headers)
			if replay.status != http.StatusOK || replay.body["output"].(map[string]any)["command_id"] != withdrawn.body["output"].(map[string]any)["command_id"] {
				t.Fatalf("replay=%s", replay.raw)
			}
			approved := routeRequest(t, handler, "route_user_a", routeApproverRole, http.MethodPost, "/workflow/tasks/"+task["id"].(string)+"/approve", map[string]any{}, nil)
			if approved.status < 400 {
				t.Fatalf("cancelled task approved=%s", approved.raw)
			}
		})
	}
}
