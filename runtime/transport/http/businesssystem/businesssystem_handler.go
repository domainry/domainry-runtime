package businesssystem

import (
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"

	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"

	"net/http"
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type RuntimeMetadata struct {
	ServiceKind        string
	RuntimeVersion     string
	APIContractVersion string
	APIContractHash    string
	ManifestHash       string
	Manifest           manifestmodel.ManifestSchema
}

type BusinessSystemHandler struct {
	service            *businesssystemapplication.BusinessSystemApplicationService
	validation         *businesssystemapplication.RuntimeAuthoringValidationApplicationService
	principal          func(*http.Request) principalmodel.Principal
	runtimeMetadata    func() RuntimeMetadata
	writeJSON          func(http.ResponseWriter, int, any)
	writeServiceError  func(http.ResponseWriter, *http.Request, error)
	decodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	beginValidation    func(string) error
	completeValidation func(string, string, bool) error
	completeDelivery   func(string, string, bool) error
}

type BusinessSystemDependencies struct {
	Service            *businesssystemapplication.BusinessSystemApplicationService
	Validation         *businesssystemapplication.RuntimeAuthoringValidationApplicationService
	Principal          func(*http.Request) principalmodel.Principal
	RuntimeMetadata    func() RuntimeMetadata
	WriteJSON          func(http.ResponseWriter, int, any)
	WriteServiceError  func(http.ResponseWriter, *http.Request, error)
	DecodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	BeginValidation    func(string) error
	CompleteValidation func(string, string, bool) error
	CompleteDelivery   func(string, string, bool) error
}

func NewBusinessSystemHandler(deps BusinessSystemDependencies) *BusinessSystemHandler {
	return &BusinessSystemHandler{
		service: deps.Service, validation: deps.Validation, principal: deps.Principal,
		runtimeMetadata: deps.RuntimeMetadata, writeJSON: deps.WriteJSON,
		writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON,
		beginValidation: deps.BeginValidation, completeValidation: deps.CompleteValidation, completeDelivery: deps.CompleteDelivery,
	}
}

func (h *BusinessSystemHandler) verifyRuntimeAuthoringDelivery(w http.ResponseWriter, r *http.Request) {
	var evidence changeplanmodel.RuntimeAuthoringDeliveryEvidence
	if h.decodeJSON == nil {
		h.writeServiceError(w, r, errors.New("business system delivery JSON decoder is unavailable"))
		return
	}
	if !h.decodeJSON(w, r, &evidence) {
		return
	}
	builderTaskID := requestcontext.RuntimeAuthoringBuilderTaskID(r.Context())
	if builderTaskID == "" || h.completeDelivery == nil {
		h.writeServiceError(w, r, errors.New("business system delivery lifecycle is unavailable"))
		return
	}
	report, err := h.validation.VerifyDelivery(r.Context(), h.principal(r), evidence)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if err := h.completeDelivery(builderTaskID, report.Binding.SnapshotHash, report.Valid); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, report)
}

func (h *BusinessSystemHandler) validateRuntimeAuthoring(w http.ResponseWriter, r *http.Request) {
	request := struct {
		Coverage *changeplanmodel.RuntimeAuthoringCoverageLedger `json:"coverage"`
	}{}
	if h.decodeJSON == nil {
		h.writeServiceError(w, r, errors.New("business system validation JSON decoder is unavailable"))
		return
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	builderTaskID := requestcontext.RuntimeAuthoringBuilderTaskID(r.Context())
	if builderTaskID != "" && h.beginValidation != nil {
		if err := h.beginValidation(builderTaskID); err != nil {
			h.writeServiceError(w, r, err)
			return
		}
	}
	result, err := h.validation.ValidateWithCoverage(r.Context(), h.principal(r), request.Coverage)
	if err != nil {
		if builderTaskID != "" && h.completeValidation != nil {
			_ = h.completeValidation(builderTaskID, "", false)
		}
		h.writeServiceError(w, r, err)
		return
	}
	if builderTaskID != "" && h.completeValidation != nil {
		if err := h.completeValidation(builderTaskID, result.SnapshotHash, result.Valid); err != nil {
			h.writeServiceError(w, r, err)
			return
		}
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *BusinessSystemHandler) Snapshot(r *http.Request) (changeplanprojection.BusinessSystemSnapshot, error) {
	principal := h.principal(r)
	metadata := h.runtimeMetadata()
	snapshot, err := h.service.Snapshot(r.Context(), principal)
	if err != nil {
		return changeplanprojection.BusinessSystemSnapshot{}, err
	}
	contract := capabilityapplication.RuntimeAuthoringCapabilities()
	snapshot = snapshot.WithRuntimeMetadata(changeplanprojection.RuntimeNativeMetadataModel{
		ModelVersion: changeplanprojection.RuntimeNativeMetadataModelVersion, Status: "installed",
		ServiceKind: metadata.ServiceKind, RuntimeVersion: metadata.RuntimeVersion,
		TemplateID: metadata.Manifest.TemplateID, TemplateVersion: metadata.Manifest.Version,
		ManifestHash: metadata.ManifestHash, SnapshotHash: metadata.ManifestHash,
		SourceBlueprintID:  metadata.Manifest.SourceBlueprintID,
		APIContractVersion: metadata.APIContractVersion, APIContractHash: metadata.APIContractHash,
		AuthoringContractVersion: contract.ContractVersion, AuthoringContractHash: contract.ContractHash,
		Manifest: &metadata.Manifest,
	})
	return snapshot, nil
}

func (h *BusinessSystemHandler) businessSystemSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.Snapshot(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	etag := `"` + snapshot.SnapshotHash + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.writeJSON(w, http.StatusOK, snapshot)
}

func builderSnapshotPrincipal(manifest manifestmodel.ManifestSchema) principalmodel.Principal {
	_ = manifest
	return principalmodel.Principal{
		Principal:          identitysdk.Principal{Known: true, UserID: "builder-snapshot", WorkspaceID: "default", RoleKey: "builder-snapshot"},
		SystemScope:        principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "builder_system_snapshot"),
		SystemCapabilities: []string{"workspace.admin"},
	}
}
