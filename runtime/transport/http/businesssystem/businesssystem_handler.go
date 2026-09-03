package businesssystem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"

	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
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
	builderTaskID := operationscontract.BuilderTaskID(r.Context())
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
		Coverage     *changeplanmodel.RuntimeAuthoringCoverageLedger `json:"coverage"`
		EvidencePlan *changeplanmodel.RuntimeAuthoringEvidencePlan   `json:"evidence_plan"`
	}{}
	if h.decodeJSON == nil {
		h.writeServiceError(w, r, errors.New("business system validation JSON decoder is unavailable"))
		return
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	if request.Coverage != nil && request.Coverage.Version == "" {
		request.Coverage.Version = changeplanmodel.RuntimeAuthoringCoverageLedgerVersion
	}
	if request.EvidencePlan != nil && request.EvidencePlan.Version == "" {
		request.EvidencePlan.Version = changeplanmodel.RuntimeAuthoringEvidencePlanVersion
	}
	builderTaskID := operationscontract.BuilderTaskID(r.Context())
	if builderTaskID != "" && h.beginValidation != nil {
		if err := h.beginValidation(builderTaskID); err != nil {
			h.writeServiceError(w, r, err)
			return
		}
	}
	var result businesssystemapplication.RuntimeAuthoringValidationReport
	var err error
	if request.EvidencePlan != nil {
		result, err = h.validation.ValidateWithCoverageAndEvidencePlan(r.Context(), h.principal(r), request.Coverage, request.EvidencePlan)
	} else if request.Coverage == nil {
		result, err = h.validation.Validate(r.Context(), h.principal(r))
	} else {
		result, err = h.validation.ValidateWithCoverage(r.Context(), h.principal(r), request.Coverage)
	}
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
	snapshot, err := h.service.Snapshot(r.Context(), principal)
	if err != nil {
		return changeplanprojection.BusinessSystemSnapshot{}, err
	}
	return snapshot.WithRuntimeMetadata(h.nativeRuntimeMetadata()), nil
}

func (h *BusinessSystemHandler) nativeRuntimeMetadata() changeplanprojection.RuntimeNativeMetadataModel {
	metadata := h.runtimeMetadata()
	contract := capabilityapplication.RuntimeAuthoringCapabilities()
	return changeplanprojection.RuntimeNativeMetadataModel{
		ModelVersion: changeplanprojection.RuntimeNativeMetadataModelVersion, Status: "installed",
		ServiceKind: metadata.ServiceKind, RuntimeVersion: metadata.RuntimeVersion,
		TemplateID: metadata.Manifest.TemplateID, TemplateVersion: metadata.Manifest.Version,
		ManifestHash: metadata.ManifestHash, SnapshotHash: metadata.ManifestHash,
		SourceBlueprintID:  metadata.Manifest.SourceBlueprintID,
		APIContractVersion: metadata.APIContractVersion, APIContractHash: metadata.APIContractHash,
		AuthoringContractVersion: contract.ContractVersion, AuthoringContractHash: contract.ContractHash,
		Manifest: &metadata.Manifest,
	}
}

func (h *BusinessSystemHandler) businessSystemSnapshot(w http.ResponseWriter, r *http.Request) {
	projection := strings.TrimSpace(r.URL.Query().Get("projection"))
	if projection == "" {
		projection = "index"
	}
	var response any
	var err error
	resourceType, resourceKey := strings.TrimSpace(r.URL.Query().Get("resource_type")), strings.TrimSpace(r.URL.Query().Get("resource_key"))
	switch projection {
	case "index":
		var index changeplanprojection.BusinessSystemSnapshotIndex
		index, err = h.service.SnapshotIndex(r.Context(), h.principal(r))
		if err == nil {
			response = index.WithRuntimeMetadata(h.nativeRuntimeMetadata())
		}
	case "resources":
		limit := changeplanprojection.BusinessSystemDefaultResourcePageSize
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			limit, err = strconv.Atoi(rawLimit)
			if err != nil {
				err = apperror.New(apperror.KindBadRequest, "backend.business_system.snapshot_resource_limit_invalid", err, map[string]string{"limit": rawLimit})
				break
			}
		}
		var page changeplanprojection.BusinessSystemResourcePage
		var valid bool
		page, valid, err = h.service.SnapshotResourcePage(r.Context(), h.principal(r), resourceType, r.URL.Query().Get("cursor"), limit)
		if err == nil && !valid {
			err = apperror.New(apperror.KindBadRequest, "backend.business_system.snapshot_resource_page_invalid", nil, map[string]string{"resource_type": resourceType, "cursor": r.URL.Query().Get("cursor"), "limit": strconv.Itoa(limit)})
		}
		if err == nil {
			response = page
		}
	case "resource":
		var detail changeplanprojection.BusinessSystemResourceDetail
		var found bool
		detail, found, err = h.service.SnapshotResourceDetail(r.Context(), h.principal(r), resourceType, resourceKey)
		if err == nil && !found {
			err = apperror.New(apperror.KindNotFound, "backend.business_system.snapshot_resource_not_found", nil, map[string]string{"resource_type": resourceType, "resource_key": resourceKey})
		}
		if err == nil {
			response = detail
		}
	case "runtime-index":
		response, err = h.service.SnapshotRuntimeStateIndex(r.Context(), h.principal(r))
	case "full":
		response, err = h.Snapshot(r)
	default:
		err = apperror.New(apperror.KindBadRequest, "backend.business_system.snapshot_projection_invalid", nil, map[string]string{"projection": projection})
	}
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	etagPayload, _ := json.Marshal(response)
	etagSum := sha256.Sum256(etagPayload)
	etag := `"` + hex.EncodeToString(etagSum[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.Header().Set("X-Domainry-Snapshot-Projection", projection)
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func builderSnapshotPrincipal(manifest manifestmodel.ManifestSchema) principalmodel.Principal {
	_ = manifest
	principal := principalmodel.NewSystemPrincipal(
		"builder-snapshot",
		principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "builder_system_snapshot"),
		businesssystemapplication.ActionBusinessSystemSnapshot,
	)
	principal.WorkspaceID = principalmodel.InstallationWorkspaceID
	principal.RoleKey = "builder-snapshot"
	return principal
}
