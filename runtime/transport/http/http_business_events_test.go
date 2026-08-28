package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	businesseventapplication "github.com/domainry/domainry-runtime/runtime/application/businessevent"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	businesseventmemory "github.com/domainry/domainry-runtime/runtime/infrastructure/broadcast/memory"
)

func TestBusinessEventPublicationMiddlewarePublishesOnlySuccessfulAuthenticatedMutations(t *testing.T) {
	service := businesseventapplication.NewBusinessEventApplicationService(businesseventmemory.NewBusinessEventBackplane(8, 4), businesseventapplication.Limits{})
	router := &HTTPRouter{businessEvents: service}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	subscription, err := service.Open(t.Context(), principal, "")
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	handler := router.withBusinessEventPublication(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	request := requestWithPrincipal(httptest.NewRequest(http.MethodPost, "/objects/customer/records", nil), principal)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	select {
	case event := <-subscription.Events:
		if event.WorkspaceID != "workspace-a" || event.ObjectKey != "customer" || event.Reason != "mutation" {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-t.Context().Done():
		t.Fatal("successful mutation did not publish")
	}

	failed := router.withBusinessEventPublication(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusConflict) }))
	failed.ServeHTTP(httptest.NewRecorder(), requestWithPrincipal(httptest.NewRequest(http.MethodPost, "/objects/customer/records", nil), principal))
	if service.Snapshot(t.Context()).PublishedTotal != 1 {
		t.Fatalf("failed mutation published event: %+v", service.Snapshot(t.Context()))
	}
}

func TestBusinessEventStatusWriterPreservesResponseController(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &businessEventStatusWriter{ResponseWriter: recorder}
	if err := http.NewResponseController(writer).Flush(); err != nil {
		t.Fatalf("flush capability lost: %v", err)
	}
}
