package businesssystem

import (
	"context"
	"errors"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func authoringHandlerValidation() *businesssystemapplication.RuntimeAuthoringValidationApplicationService {
	return businesssystemapplication.NewRuntimeAuthoringValidationApplicationService(businesssystemapplication.RuntimeAuthoringValidationDependencies{
		CurrentManifest: func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
			return manifestmodel.ManifestSchema{SchemaVersion: "2", TemplateID: "direct", Version: "configuring", Objects: []definitionmodel.ObjectSchema{}}, nil
		},
		CurrentSnapshot: func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
			return businessSystemCompleteValidationSnapshot(), nil
		},
		ValidateDefinitions: func(context.Context, []integrationmodel.ConnectorSchema) error { return nil },
	})
}

func authoringHandlerForEdges(validation *businesssystemapplication.RuntimeAuthoringValidationApplicationService) *BusinessSystemHandler {
	return NewBusinessSystemHandler(BusinessSystemDependencies{
		Validation: validation,
		Principal: func(*http.Request) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "builder", WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
		},
		WriteJSON:         func(w http.ResponseWriter, status int, _ any) { w.WriteHeader(status) },
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusInternalServerError) },
		DecodeJSON:        func(_ http.ResponseWriter, _ *http.Request, _ any) bool { return true },
	})
}

func authoringRequest(taskID string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/domain-system-authoring", nil)
	if taskID != "" {
		request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), taskID))
	}
	return request
}

func TestRuntimeAuthoringDeliveryHandlerEdges(t *testing.T) {
	t.Run("decoder unavailable", func(t *testing.T) {
		handler := authoringHandlerForEdges(authoringHandlerValidation())
		handler.decodeJSON = nil
		response := httptest.NewRecorder()
		handler.verifyRuntimeAuthoringDelivery(response, authoringRequest("task"))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})
	t.Run("decode rejected", func(t *testing.T) {
		handler := authoringHandlerForEdges(authoringHandlerValidation())
		handler.decodeJSON = func(http.ResponseWriter, *http.Request, any) bool { return false }
		response := httptest.NewRecorder()
		handler.verifyRuntimeAuthoringDelivery(response, authoringRequest("task"))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d", response.Code)
		}
	})
	for _, test := range []struct {
		name, task string
		completion func(string, string, bool) error
	}{
		{name: "missing task", completion: func(string, string, bool) error { return nil }},
		{name: "missing completion", task: "task"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := authoringHandlerForEdges(authoringHandlerValidation())
			handler.completeDelivery = test.completion
			response := httptest.NewRecorder()
			handler.verifyRuntimeAuthoringDelivery(response, authoringRequest(test.task))
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
	t.Run("validation failed", func(t *testing.T) {
		handler := authoringHandlerForEdges(businesssystemapplication.NewRuntimeAuthoringValidationApplicationService(businesssystemapplication.RuntimeAuthoringValidationDependencies{}))
		handler.completeDelivery = func(string, string, bool) error { return nil }
		response := httptest.NewRecorder()
		handler.verifyRuntimeAuthoringDelivery(response, authoringRequest("task"))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})
	for _, completionErr := range []error{errors.New("complete"), nil} {
		name := "success"
		if completionErr != nil {
			name = "completion failed"
		}
		t.Run(name, func(t *testing.T) {
			handler := authoringHandlerForEdges(authoringHandlerValidation())
			handler.completeDelivery = func(string, string, bool) error { return completionErr }
			response := httptest.NewRecorder()
			handler.verifyRuntimeAuthoringDelivery(response, authoringRequest("task"))
			want := http.StatusOK
			if completionErr != nil {
				want = http.StatusInternalServerError
			}
			if response.Code != want {
				t.Fatalf("status=%d want=%d", response.Code, want)
			}
		})
	}
}

func TestRuntimeAuthoringValidationHandlerEdges(t *testing.T) {
	t.Run("decoder unavailable", func(t *testing.T) {
		handler := authoringHandlerForEdges(authoringHandlerValidation())
		handler.decodeJSON = nil
		response := httptest.NewRecorder()
		handler.validateRuntimeAuthoring(response, authoringRequest("task"))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})
	t.Run("decode rejected", func(t *testing.T) {
		handler := authoringHandlerForEdges(authoringHandlerValidation())
		handler.decodeJSON = func(http.ResponseWriter, *http.Request, any) bool { return false }
		response := httptest.NewRecorder()
		handler.validateRuntimeAuthoring(response, authoringRequest("task"))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d", response.Code)
		}
	})
	t.Run("begin failed", func(t *testing.T) {
		handler := authoringHandlerForEdges(authoringHandlerValidation())
		handler.beginValidation = func(string) error { return errors.New("begin") }
		response := httptest.NewRecorder()
		handler.validateRuntimeAuthoring(response, authoringRequest("task"))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})
	t.Run("validation failed and cleanup", func(t *testing.T) {
		handler := authoringHandlerForEdges(businesssystemapplication.NewRuntimeAuthoringValidationApplicationService(businesssystemapplication.RuntimeAuthoringValidationDependencies{}))
		handler.beginValidation = func(string) error { return nil }
		cleaned := false
		handler.completeValidation = func(string, string, bool) error { cleaned = true; return nil }
		response := httptest.NewRecorder()
		handler.validateRuntimeAuthoring(response, authoringRequest("task"))
		if response.Code != http.StatusInternalServerError || !cleaned {
			t.Fatalf("status=%d cleaned=%v", response.Code, cleaned)
		}
	})
	for _, taskID := range []string{"", "task"} {
		name := "validation failed without task"
		if taskID != "" {
			name = "validation failed without completion callback"
		}
		t.Run(name, func(t *testing.T) {
			handler := authoringHandlerForEdges(businesssystemapplication.NewRuntimeAuthoringValidationApplicationService(businesssystemapplication.RuntimeAuthoringValidationDependencies{}))
			response := httptest.NewRecorder()
			handler.validateRuntimeAuthoring(response, authoringRequest(taskID))
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
	t.Run("success without lifecycle", func(t *testing.T) {
		handler := authoringHandlerForEdges(authoringHandlerValidation())
		handler.beginValidation = func(string) error { return nil }
		handler.completeValidation = func(string, string, bool) error { return nil }
		response := httptest.NewRecorder()
		handler.validateRuntimeAuthoring(response, authoringRequest(""))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d", response.Code)
		}
	})
	t.Run("success with nil callbacks", func(t *testing.T) {
		handler := authoringHandlerForEdges(authoringHandlerValidation())
		response := httptest.NewRecorder()
		handler.validateRuntimeAuthoring(response, authoringRequest("task"))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d", response.Code)
		}
	})
	t.Run("completion failed", func(t *testing.T) {
		handler := authoringHandlerForEdges(authoringHandlerValidation())
		handler.completeValidation = func(string, string, bool) error { return errors.New("complete") }
		response := httptest.NewRecorder()
		handler.validateRuntimeAuthoring(response, authoringRequest("task"))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})
}
