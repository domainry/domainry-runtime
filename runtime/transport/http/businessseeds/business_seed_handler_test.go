package businessseeds

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type businessSeedHandlerService struct {
	result businessseedmodel.SeedRecordAuthoringResult
	err    error
	key    string
	apply  bool
}

func (s *businessSeedHandlerService) Validate(_ context.Context, key string, _ businessseedmodel.SeedRecordAuthoringRequest, _ principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error) {
	s.key = key
	return s.result, s.err
}
func (s *businessSeedHandlerService) Apply(_ context.Context, key string, _ businessseedmodel.SeedRecordAuthoringRequest, _ principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error) {
	s.key, s.apply = key, true
	return s.result, s.err
}
func (s *businessSeedHandlerService) Get(_ context.Context, key string, _ principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error) {
	s.key = key
	return s.result, s.err
}
func (s *businessSeedHandlerService) Versions(_ context.Context, key string, _ principalmodel.Principal) ([]businessseedmodel.SeedRecordAuthoringResult, error) {
	s.key = key
	return []businessseedmodel.SeedRecordAuthoringResult{s.result}, s.err
}
func (s *businessSeedHandlerService) AuthorizeSeedRecordUpsert(principalmodel.Principal) error {
	return s.err
}
func (s *businessSeedHandlerService) SeedRecordAuthoringHash(context.Context, string, principalmodel.Principal) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	if s.apply {
		return "seed-hash", true, nil
	}
	return "", false, nil
}

type businessSeedOperationsRepository struct {
	receipts map[string]operationsmodel.OperationsReceipt
}

func (r *businessSeedOperationsRepository) RegisterOperationsCommand(_ context.Context, receipt operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	r.receipts[receipt.Command.ID] = receipt
	return receipt, operationsmodel.OperationsSubmissionAccepted, nil
}
func (r *businessSeedOperationsRepository) GetOperationsReceipt(_ context.Context, _ operationsmodel.OperationsScope, id string) (operationsmodel.OperationsReceipt, bool, error) {
	receipt, found := r.receipts[id]
	return receipt, found, nil
}
func (*businessSeedOperationsRepository) ListOperationsReceipts(context.Context, operationsmodel.OperationsScope, operationsmodel.OperationsStatus, int) ([]operationsmodel.OperationsReceipt, error) {
	return nil, nil
}
func (*businessSeedOperationsRepository) SearchOperationsReceipts(context.Context, operationsmodel.OperationsScope, operationsmodel.OperationsReceiptFilter) (operationsmodel.OperationsReceiptPage, error) {
	return operationsmodel.OperationsReceiptPage{Items: []operationsmodel.OperationsReceipt{}}, nil
}
func (r *businessSeedOperationsRepository) UpdateOperationsReceipt(_ context.Context, receipt operationsmodel.OperationsReceipt, expected operationsmodel.OperationsStatus) (bool, error) {
	current, found := r.receipts[receipt.Command.ID]
	if !found || current.Command.Status != expected {
		return false, nil
	}
	r.receipts[receipt.Command.ID] = receipt
	return true, nil
}

func TestBusinessSeedHandlerRoutesValidateAndApply(t *testing.T) {
	service := &businessSeedHandlerService{result: businessseedmodel.SeedRecordAuthoringResult{SeedKey: "customer.acme"}}
	var serviceErr error
	handler := NewBusinessSeedHandler(BusinessSeedDependencies{
		Service: service, Principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
		},
		DecodeJSON: func(_ http.ResponseWriter, r *http.Request, value any) bool {
			return json.NewDecoder(r.Body).Decode(value) == nil
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, test := range []struct {
		method string
		path   string
		apply  bool
	}{{http.MethodPost, "/business-seeds/customer.acme/validate", false}, {http.MethodPut, "/business-seeds/customer.acme", true}, {http.MethodGet, "/business-seeds/customer.acme", false}, {http.MethodGet, "/business-seeds/customer.acme/versions", false}} {
		service.apply = false
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader("{\"seed_key\":\"customer.acme\",\"object_key\":\"customer\",\"data\":{\"name\":\"Acme\"}}"))
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK || service.key != "customer.acme" || service.apply != test.apply {
			t.Fatalf("method=%s status=%d key=%s apply=%v", test.method, response.Code, service.key, service.apply)
		}
		if strings.HasSuffix(test.path, "/versions") && !strings.Contains(response.Body.String(), `"versioning":"immutable_singleton"`) {
			t.Fatalf("versions body=%s", response.Body.String())
		}
	}
	service.err = errors.New("failure")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/business-seeds/customer.acme", strings.NewReader("{\"object_key\":\"customer\",\"data\":{}}")))
	if response.Code != http.StatusUnprocessableEntity || !errors.Is(serviceErr, service.err) {
		t.Fatalf("status=%d err=%v", response.Code, serviceErr)
	}
}

func TestBusinessSeedHandlerCoversDecodeAndServiceFailures(t *testing.T) {
	want := errors.New("business seed unavailable")
	service := &businessSeedHandlerService{err: want}
	var serviceErr error
	decode := true
	handler := NewBusinessSeedHandler(BusinessSeedDependencies{
		Service: service, Principal: func(*http.Request) principalmodel.Principal { return principalmodel.Principal{} },
		DecodeJSON: func(http.ResponseWriter, *http.Request, any) bool { return decode },
		WriteJSON:  func(http.ResponseWriter, int, any) {},
		WriteServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) {
			serviceErr = err
		},
	})
	for _, call := range []func(http.ResponseWriter, *http.Request){handler.get, handler.versions, handler.validate, handler.apply} {
		serviceErr = nil
		request := httptest.NewRequest(http.MethodPost, "/business-seeds/seed", strings.NewReader(`{}`))
		request.SetPathValue("seedKey", "seed")
		call(httptest.NewRecorder(), request)
		if !errors.Is(serviceErr, want) {
			t.Fatalf("service error=%v", serviceErr)
		}
	}
	service.err = nil
	decode = false
	for _, call := range []func(http.ResponseWriter, *http.Request){handler.validate, handler.apply} {
		service.apply = false
		call(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/business-seeds/seed", strings.NewReader(`{`)))
		if service.apply {
			t.Fatal("decode rejection reached service")
		}
	}
}

func TestBusinessSeedHandlerUsesOperationsEnvelope(t *testing.T) {
	service := &businessSeedHandlerService{result: businessseedmodel.SeedRecordAuthoringResult{SeedKey: "asset.primary"}}
	repository := &businessSeedOperationsRepository{receipts: map[string]operationsmodel.OperationsReceipt{}}
	operations := operationsapplication.NewOperationsApplicationService(repository, nil, nil, func() string { return "seed-operation" })
	operations.UseDirectAuthoringProjection(func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{SnapshotHash: "snapshot-hash"}, nil
	})
	var serviceErr error
	handler := NewBusinessSeedHandler(BusinessSeedDependencies{
		Service: service, Operations: operations,
		Principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "builder", WorkspaceID: "workspace"}}
		},
		DecodeJSON: func(_ http.ResponseWriter, request *http.Request, value any) bool {
			return json.NewDecoder(request.Body).Decode(value) == nil
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) { serviceErr = err },
	})
	request := httptest.NewRequest(http.MethodPut, "/business-seeds/asset.primary", strings.NewReader(`{"object_key":"asset","data":{}}`))
	request.SetPathValue("seedKey", " asset.primary ")
	request.Header.Set("Builder-Task-ID", " task-1 ")
	request.Header.Set("Idempotency-Key", " command-1 ")
	request.Header.Set("Expected-Schema-Hash", " empty ")
	response := httptest.NewRecorder()
	handler.apply(response, request)
	if response.Code != http.StatusOK || serviceErr != nil || !service.apply || !strings.Contains(response.Body.String(), `"seed_key":"asset.primary"`) {
		t.Fatalf("status=%d apply=%v error=%v body=%s", response.Code, service.apply, serviceErr, response.Body.String())
	}

	want := errors.New("authoring denied")
	service.err, service.apply, serviceErr = want, false, nil
	request = httptest.NewRequest(http.MethodPut, "/business-seeds/asset.primary", strings.NewReader(`{"object_key":"asset","data":{}}`))
	request.SetPathValue("seedKey", "asset.primary")
	request.Header.Set("Builder-Task-ID", "task-1")
	request.Header.Set("Idempotency-Key", "command-2")
	request.Header.Set("Expected-Schema-Hash", "seed-hash")
	handler.apply(httptest.NewRecorder(), request)
	if !errors.Is(serviceErr, want) || service.apply {
		t.Fatalf("error=%v apply=%v", serviceErr, service.apply)
	}
}
