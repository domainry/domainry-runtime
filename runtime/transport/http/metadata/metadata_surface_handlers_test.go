package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type opsMetadataRepository struct {
	metadatarepository.MetadataRepository
	revision string
	err      error
}

func (repository *opsMetadataRepository) SnapshotRevision(context.Context, principalmodel.SystemScope) (string, error) {
	return repository.revision, repository.err
}

func TestOpsMetadataDiagnosticsHandlerSuccessAndServiceError(t *testing.T) {
	repository := &opsMetadataRepository{revision: "revision-1"}
	service := metadataapplication.NewMetadataApplicationService(metadataapplication.MetadataApplicationDependencies{
		Repository: repository,
		Runtime: localizedTextHandlerRuntime{snapshot: metadatamodel.MetadataSchemaSnapshot{
			SchemaHash: "schema-hash", SnapshotVersion: "snapshot-1", TemplateVersion: "1",
			Objects: []definitionmodel.ObjectSchema{{Key: "customer"}},
		}},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{metadataapplication.PermissionMetadataOpsRead}})
	var serviceErr error
	handler := NewMetadataHandler(MetadataDependencies{
		RuntimeCatalog: service,
		Principal:      func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
	})

	response := httptest.NewRecorder()
	handler.opsMetadataDiagnostics(response, httptest.NewRequest(http.MethodGet, "/ops/metadata/diagnostics", nil))
	if response.Code != http.StatusOK || serviceErr != nil ||
		!strings.Contains(response.Body.String(), `"repository_revision":"revision-1"`) ||
		!strings.Contains(response.Body.String(), `"compatible":true`) {
		t.Fatalf("status=%d error=%v body=%s", response.Code, serviceErr, response.Body.String())
	}

	repository.err = errors.New("revision unavailable")
	response = httptest.NewRecorder()
	handler.opsMetadataDiagnostics(response, httptest.NewRequest(http.MethodGet, "/ops/metadata/diagnostics", nil))
	if response.Code != http.StatusUnprocessableEntity || serviceErr == nil {
		t.Fatalf("status=%d error=%v", response.Code, serviceErr)
	}
}
