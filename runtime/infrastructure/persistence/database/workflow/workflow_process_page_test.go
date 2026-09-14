package workflow

import (
	"encoding/json"
	"fmt"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowhttp "github.com/domainry/domainry-runtime/runtime/transport/http/workflows"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestWorkflowProcessPagesTraverseBeyond500WithTiesAndLiveVisibility(t *testing.T) {
	worker := openWorkflowWorkerEdgeStore(t)
	repository := NewWorkflowProcessStore(worker.store)
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace-a", AuthorizationRevision: "revision-1"}}
	createdAt := "2026-09-14T00:00:00Z"
	for i := 0; i < 550; i++ {
		user := "user"
		if i >= 501 {
			user = "other"
		}
		process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-a", ID: fmt.Sprintf("process_%04d", i), WorkflowKey: "approval", ObjectKey: "application", InitiatorID: user, Status: "waiting", CreatedAt: createdAt, UpdatedAt: createdAt}
		if err := repository.InsertProcess(t.Context(), "workspace-a", process); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.InsertProcess(t.Context(), "workspace-b", workflowmodel.WorkflowProcessInstance{ID: "foreign", WorkflowKey: "approval", ObjectKey: "application", InitiatorID: "user", Status: "waiting", CreatedAt: createdAt, UpdatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	service := workflowapplication.NewWorkflowApplicationService(workflowapplication.WorkflowDependencies{Processes: repository})
	handler := workflowhttp.NewWorkflowsHandler(workflowhttp.WorkflowsDependencies{Processes: service, Principal: func(*http.Request) principalmodel.Principal { return principal }, WriteJSON: func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}, WriteServiceError: func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	cursor := ""
	seen := map[string]bool{}
	for number := 0; number < 4; number++ {
		recorder := httptest.NewRecorder()
		target := "/workflow/processes?object_key=application&page_size=200&cursor=" + url.QueryEscape(cursor)
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		var page workflowapplication.ParticipantWorkflowProcessPageDTO
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &page) != nil {
			t.Fatalf("page=%s", recorder.Body.String())
		}
		for _, item := range page.Items {
			if seen[item.ID] || item.InitiatorID != "user" || item.ID == "foreign" {
				t.Fatalf("duplicate or unauthorized process=%#v", item)
			}
			seen[item.ID] = true
		}
		if number == 0 {
			// New rows above the first page cannot shift the following keyset pages.
			if err := repository.InsertProcess(t.Context(), "workspace-a", workflowmodel.WorkflowProcessInstance{ID: "new_after_first_page", WorkflowKey: "approval", ObjectKey: "application", InitiatorID: "user", Status: "waiting", CreatedAt: "2026-09-15T00:00:00Z", UpdatedAt: createdAt}); err != nil {
				t.Fatal(err)
			}
			for _, filter := range []workflowmodel.WorkflowProcessFilter{{ObjectKey: "different", Cursor: page.NextCursor}, {ObjectKey: "application", Cursor: "invalid"}} {
				if _, err := service.ParticipantWorkflowProcessPage(t.Context(), principal, filter); err == nil {
					t.Fatal("changed filter or invalid cursor accepted")
				}
			}
			changed := principal
			changed.AuthorizationRevision = "revision-2"
			if _, err := service.ParticipantWorkflowProcessPage(t.Context(), changed, workflowmodel.WorkflowProcessFilter{ObjectKey: "application", Cursor: page.NextCursor}); err == nil {
				t.Fatal("changed authorization revision accepted")
			}
			changed = principal
			changed.UserID = "other"
			if _, err := service.ParticipantWorkflowProcessPage(t.Context(), changed, workflowmodel.WorkflowProcessFilter{ObjectKey: "application", Cursor: page.NextCursor}); err == nil {
				t.Fatal("different caller accepted cursor")
			}
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatal("terminal page supplied cursor")
			}
			break
		}
		if len(page.Items) != 200 || page.NextCursor == "" {
			t.Fatalf("incomplete continuation page=%#v", page)
		}
		cursor = page.NextCursor
	}
	if len(seen) != 501 {
		t.Fatalf("traversed=%d want=501", len(seen))
	}
	legacy := httptest.NewRecorder()
	mux.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/workflow/processes?object_key=application&limit=7", nil))
	var items []workflowapplication.ParticipantWorkflowProcessDTO
	if legacy.Code != http.StatusOK || json.Unmarshal(legacy.Body.Bytes(), &items) != nil || len(items) != 7 {
		t.Fatalf("legacy array=%s", legacy.Body.String())
	}
}
