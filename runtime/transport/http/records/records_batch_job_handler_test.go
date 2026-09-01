package records

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/modulecapability"
	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type recordBatchHTTPFixture struct {
	handler   *RecordsHandler
	mux       *http.ServeMux
	principal *principalmodel.Principal
	exchange  *recordBatchDataExchangeProbe
}

func newRecordBatchHTTPFixture(t *testing.T) recordBatchHTTPFixture {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "record-batch-http.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text", Required: true}}}
	if err := appschemapersistence.NewApplicationSchemaStore(store).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "prepare record batch test storage"), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	role := accessfixture.Bundle{
		Key: "admin", Permissions: []string{"customer.*"}, RecordScope: "all_records",
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true, Write: true}},
	}
	exchange := &recordBatchDataExchangeProbe{}
	services := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{
		TemplateID: "record-batch-http", TemplateVersion: "1", Name: "Record Batch HTTP",
		Objects: []definitionmodel.ObjectSchema{object}, Store: store, DataExchange: exchange,
	})
	principal := accessfixture.AttachPointer(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, role)
	handler := NewRecordsHandler(RecordsDependencies{
		Queries:   services.Applications().Records,
		Principal: func(*http.Request) principalmodel.Principal { return *principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			t.Logf("record HTTP service error: %v", err)
			status := http.StatusInternalServerError
			switch apperror.KindOf(err) {
			case apperror.KindBadRequest:
				status = http.StatusBadRequest
			case apperror.KindForbidden:
				status = http.StatusForbidden
			case apperror.KindNotFound:
				status = http.StatusNotFound
			case apperror.KindConflict:
				status = http.StatusConflict
			case apperror.KindRateLimited:
				status = http.StatusTooManyRequests
			case apperror.KindUnavailable:
				status = http.StatusServiceUnavailable
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": apperror.CodeOf(err)})
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, value any) bool {
			if err := json.NewDecoder(r.Body).Decode(value); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return recordBatchHTTPFixture{handler: handler, mux: mux, principal: principal, exchange: exchange}
}

type recordBatchDataExchangeProbe struct {
	modulecapability.Binding
	mu           sync.Mutex
	jobs         map[string]dataexchange.Job
	fingerprints map[string][]byte
	lastExport   dataexchange.ExportRequest
	artifact     dataexchange.Artifact
	content      string
}

func (*recordBatchDataExchangeProbe) Descriptor() dataexchange.Descriptor {
	return dataexchange.Descriptor{ProtocolVersion: dataexchange.ProtocolVersionV1, Mode: dataexchange.DeploymentModeModule}
}

func (p *recordBatchDataExchangeProbe) SubmitImport(_ context.Context, request dataexchange.ImportRequest) (dataexchange.Job, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(request.Source, request.MaxBytes+1))
	if err != nil || int64(len(raw)) > request.MaxBytes {
		return dataexchange.Job{}, false, dataexchange.ErrSourceUnreadable
	}
	if _, err := csv.NewReader(bytes.NewReader(raw)).ReadAll(); err != nil {
		return dataexchange.Job{}, false, dataexchange.ErrSourceUnreadable
	}
	return p.submit(request.Scope, request.Provider, "import", request.ObjectKey, request.IdempotencyKey, raw)
}

func (p *recordBatchDataExchangeProbe) SubmitExport(_ context.Context, request dataexchange.ExportRequest) (dataexchange.Job, bool, error) {
	p.mu.Lock()
	p.lastExport = request
	p.mu.Unlock()
	return p.submit(request.Scope, request.Provider, "export", request.ObjectKey, request.IdempotencyKey, request.Options)
}

func (p *recordBatchDataExchangeProbe) submit(scope dataexchange.Scope, provider, operation, objectKey, key string, fingerprint []byte) (dataexchange.Job, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.jobs == nil {
		p.jobs, p.fingerprints = map[string]dataexchange.Job{}, map[string][]byte{}
	}
	id := "data_exchange:" + scope.WorkspaceID + ":" + operation + ":" + key
	if job, found := p.jobs[id]; found {
		if !bytes.Equal(p.fingerprints[id], fingerprint) {
			return dataexchange.Job{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, nil, nil)
		}
		return job, true, nil
	}
	now := time.Now().UTC()
	job := dataexchange.Job{ID: id, Provider: provider, Operation: operation, Status: "queued", WorkspaceID: scope.WorkspaceID, ObjectKey: objectKey, ActorID: scope.ActorID, RoleKey: scope.RoleKey, Options: append([]byte(nil), fingerprint...), CreatedAt: now, UpdatedAt: now}
	p.jobs[id], p.fingerprints[id] = job, append([]byte(nil), fingerprint...)
	return job, false, nil
}

func (p *recordBatchDataExchangeProbe) Job(_ context.Context, request dataexchange.JobRequest) (dataexchange.Job, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	job, found := p.jobs[request.JobID]
	if !found || job.WorkspaceID != request.Scope.WorkspaceID || job.ActorID != request.Scope.ActorID {
		return dataexchange.Job{}, apperror.New(apperror.KindNotFound, "data_exchange.job_not_found", nil, nil)
	}
	return job, nil
}

func (p *recordBatchDataExchangeProbe) Cancel(ctx context.Context, request dataexchange.JobRequest) (dataexchange.Job, error) {
	job, err := p.Job(ctx, request)
	if err != nil {
		return dataexchange.Job{}, err
	}
	p.mu.Lock()
	job.Status, job.UpdatedAt = "cancelled", time.Now().UTC()
	p.jobs[job.ID] = job
	p.mu.Unlock()
	return job, nil
}

func (p *recordBatchDataExchangeProbe) Download(ctx context.Context, request dataexchange.JobRequest) (dataexchange.Artifact, error) {
	job, err := p.Job(ctx, request)
	if err != nil {
		return dataexchange.Artifact{}, err
	}
	if job.Status != "completed" {
		return dataexchange.Artifact{}, apperror.New(apperror.KindConflict, "data_exchange.result_not_ready", nil, nil)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	artifact := p.artifact
	artifact.Content = io.NopCloser(strings.NewReader(p.content))
	return artifact, nil
}

func (*recordBatchDataExchangeProbe) Start(context.Context, dataexchange.WorkerConfig) <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func (*recordBatchDataExchangeProbe) Close(context.Context) error { return nil }

func (f recordBatchHTTPFixture) call(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	return response
}

func TestRecordBatchJobHTTPExportSubmission(t *testing.T) {
	fixture := newRecordBatchHTTPFixture(t)
	if response := fixture.call(http.MethodPost, "/objects/customer/records/export/jobs", "", nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency status=%d body=%s", response.Code, response.Body.String())
	}

	path := "/objects/customer/records/export/jobs?fields=name&reason=audit&masking_policy=strict&filter_summary=active&page=2&page_size=25"
	created := fixture.call(http.MethodPost, path, "", map[string]string{"Idempotency-Key": "export-http-1"})
	var job recordmodel.RecordBatchJob
	if created.Code != http.StatusAccepted || json.Unmarshal(created.Body.Bytes(), &job) != nil || job.ID == "" || job.Kind != "export" {
		t.Fatalf("created status=%d job=%+v body=%s", created.Code, job, created.Body.String())
	}
	if created.Header().Get("Location") != "/data-exchange/jobs/"+job.ID+"?provider=records&operation=export" {
		t.Fatalf("location=%q", created.Header().Get("Location"))
	}
	var payload struct {
		Options recordapplication.RecordExportOptions `json:"options"`
	}
	if err := json.Unmarshal(fixture.exchange.lastExport.Options, &payload); err != nil || fixture.exchange.lastExport.Provider != "records" || len(payload.Options.Fields) != 1 || payload.Options.Fields[0] != "name" || payload.Options.Reason != "audit" || payload.Options.MaskingPolicy != "strict" || payload.Options.FilterSummary != "active" || payload.Options.Query.Page != 2 || payload.Options.Query.PageSize != 25 {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}

	replay := fixture.call(http.MethodPost, path, "", map[string]string{"Idempotency-Key": "export-http-1"})
	if replay.Code != http.StatusAccepted || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay status=%d headers=%v body=%s", replay.Code, replay.Header(), replay.Body.String())
	}
	conflict := fixture.call(http.MethodPost, "/objects/customer/records/export/jobs?fields=name&reason=changed", "", map[string]string{"Idempotency-Key": "export-http-1"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestRecordBatchJobHTTPImportSubmissionAndDownloadState(t *testing.T) {
	fixture := newRecordBatchHTTPFixture(t)
	if response := fixture.call(http.MethodPost, "/objects/customer/records/import/jobs", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid json status=%d body=%s", response.Code, response.Body.String())
	}
	if response := fixture.call(http.MethodPost, "/objects/customer/records/import/jobs", `{"csv":""}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("empty csv status=%d body=%s", response.Code, response.Body.String())
	}
	if response := fixture.call(http.MethodPost, "/objects/customer/records/import/jobs", "Name\nAcme\n", map[string]string{"Content-Type": "text/csv"}); response.Code != http.StatusBadRequest {
		t.Fatalf("missing key status=%d body=%s", response.Code, response.Body.String())
	}
	if response := fixture.call(http.MethodPost, "/objects/customer/records/import/jobs", "Name\n\"unterminated", map[string]string{"Content-Type": "text/csv", "Idempotency-Key": "invalid-import"}); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid csv status=%d body=%s", response.Code, response.Body.String())
	}
	created := fixture.call(http.MethodPost, "/objects/customer/records/import/jobs", "Name\nAcme\n", map[string]string{"Content-Type": "text/csv", "Idempotency-Key": "import-http-1"})
	var job recordmodel.RecordBatchJob
	if created.Code != http.StatusAccepted || json.Unmarshal(created.Body.Bytes(), &job) != nil || job.Kind != "import" {
		t.Fatalf("import status=%d job=%+v body=%s", created.Code, job, created.Body.String())
	}
	if created.Header().Get("Location") != "/data-exchange/jobs/"+job.ID+"?provider=records&operation=import" {
		t.Fatalf("location=%q", created.Header().Get("Location"))
	}
}

func TestSetBatchCapacityRetryAfter(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "rate limited", err: apperror.New(apperror.KindRateLimited, "capacity.rate", nil, nil), want: "5"},
		{name: "unavailable", err: apperror.New(apperror.KindUnavailable, "capacity.queue", nil, nil), want: "5"},
		{name: "other", err: apperror.New(apperror.KindBadRequest, "bad", nil, nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			setBatchCapacityRetryAfter(response, test.err)
			if got := response.Header().Get("Retry-After"); got != test.want {
				t.Fatalf("Retry-After=%q want=%q", got, test.want)
			}
		})
	}
}
