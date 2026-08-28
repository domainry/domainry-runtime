package principal

import (
	"context"
	"fmt"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type BusinessPrincipalDependencies struct {
	Records    recordrepository.RecordRepository
	Objects    func() []definitionmodel.ObjectSchema
	Extensions func() []profilebindingmodel.Binding
}

// BusinessPrincipalApplicationService resolves business identity facts from
// the current published object graph on every request. It intentionally owns
// no authorization cache: a committed profile mutation changes the next
// principal and its AuthorizationRevision immediately.
type BusinessPrincipalApplicationService struct {
	dependencies BusinessPrincipalDependencies
}

func NewBusinessPrincipalApplicationService(dependencies BusinessPrincipalDependencies) *BusinessPrincipalApplicationService {
	return &BusinessPrincipalApplicationService{dependencies: dependencies}
}

func (s *BusinessPrincipalApplicationService) ResolveBusinessPrincipal(ctx context.Context, principal principalmodel.Principal, surfaceKey, bindingKey, recordID string) (principalmodel.Principal, error) {
	if err := ctx.Err(); err != nil {
		return principalmodel.Principal{}, err
	}
	if !principal.Known || s == nil || s.dependencies.Records == nil || s.dependencies.Extensions == nil || s.dependencies.Objects == nil {
		return principal, nil
	}
	objects := map[string]definitionmodel.ObjectSchema{}
	for _, object := range s.dependencies.Objects() {
		objects[object.Key] = object
	}
	extensions := append([]profilebindingmodel.Binding(nil), s.dependencies.Extensions()...)
	sort.Slice(extensions, func(i, j int) bool { return extensions[i].BusinessIdentity.Key < extensions[j].BusinessIdentity.Key })
	profiles := []profilebindingmodel.Reference{}
	revisions := []map[string]any{}
	for _, extension := range extensions {
		object, ok := objects[extension.ObjectKey]
		if !ok {
			return principalmodel.Principal{}, businessPrincipalError(apperror.KindInternal, "backend.identity.business_profile_contract_invalid", fmt.Errorf("unknown profile object %q", extension.ObjectKey))
		}
		page, err := s.dependencies.Records.ListRecords(ctx, principal.WorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 2, Filters: map[string]any{extension.IdentityRelationField: principal.UserID}, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}})
		if err != nil {
			return principalmodel.Principal{}, businessPrincipalError(apperror.KindInternal, "backend.identity.business_profile_resolution_failed", err)
		}
		if page.Total > 1 || len(page.Items) > 1 {
			return principalmodel.Principal{}, businessPrincipalError(apperror.KindConflict, "backend.identity.business_profile_cardinality", fmt.Errorf("binding %q resolved multiple records", extension.BusinessIdentity.Key))
		}
		if len(page.Items) == 0 {
			continue
		}
		record := page.Items[0]
		active, err := businessProfileActive(extension.BusinessIdentity, record.Data)
		if err != nil {
			return principalmodel.Principal{}, err
		}
		if !active {
			continue
		}
		claims := map[string]profilebindingmodel.ClaimValue{}
		for _, claim := range extension.BusinessIdentity.Claims {
			field, found := profileField(object, claim.FieldKey)
			if !found {
				return principalmodel.Principal{}, businessPrincipalError(apperror.KindInternal, "backend.identity.business_profile_contract_invalid", fmt.Errorf("unknown claim field %q", claim.FieldKey))
			}
			claims[claim.ClaimKey] = profilebindingmodel.ClaimValue{Type: businessClaimType(field.Type), Value: record.Data[claim.FieldKey]}
		}
		profile := profilebindingmodel.Reference{BindingKey: extension.BusinessIdentity.Key, ObjectKey: extension.ObjectKey, RecordID: record.ID, SurfaceKeys: append([]string(nil), extension.BusinessIdentity.SurfaceKeys...), Claims: claims}
		profiles = append(profiles, profile)
		revisions = append(revisions, map[string]any{"binding_key": profile.BindingKey, "object_key": profile.ObjectKey, "record_id": profile.RecordID, "updated_at": record.UpdatedAt, "claims": claims})
	}
	principal.BusinessProfiles = profiles
	principal.BusinessClaims = nil
	principal.ActiveBusinessProfile = nil
	principal.SurfaceKey = ""
	revision, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "identity.business_principal", ResourceType: principal.WorkspaceID, TargetID: principal.UserID,
		Payload: map[string]any{"base_authorization_revision": principal.AuthorizationRevision, "business_profiles": revisions},
	})
	if err != nil {
		return principalmodel.Principal{}, businessPrincipalError(apperror.KindInternal, "backend.identity.business_profile_resolution_failed", err)
	}
	principal.AuthorizationRevision = revision
	selected, found, err := selectBusinessProfile(profiles, surfaceKey, bindingKey, recordID)
	if err != nil {
		return principalmodel.Principal{}, err
	}
	if found {
		selectedCopy := selected
		principal.ActiveBusinessProfile = &selectedCopy
		principal.BusinessClaims = selectedCopy.Claims
		principal.SurfaceKey = strings.TrimSpace(surfaceKey)
	}
	return principal, nil
}

func businessProfileActive(binding profilebindingmodel.BusinessIdentityBinding, data map[string]any) (bool, error) {
	if binding.BlacklistField != "" {
		blacklisted, ok := data[binding.BlacklistField].(bool)
		if !ok {
			return false, businessPrincipalError(apperror.KindInternal, "backend.identity.business_profile_fact_invalid", fmt.Errorf("blacklist field %q is not boolean", binding.BlacklistField))
		}
		if blacklisted {
			return false, nil
		}
	}
	if binding.StatusField == "" {
		return true, nil
	}
	status := strings.TrimSpace(fmt.Sprint(data[binding.StatusField]))
	for _, allowed := range binding.ActiveStatusValues {
		if status == strings.TrimSpace(allowed) {
			return true, nil
		}
	}
	return false, nil
}

func selectBusinessProfile(profiles []profilebindingmodel.Reference, surfaceKey, bindingKey, recordID string) (profilebindingmodel.Reference, bool, error) {
	surfaceKey, bindingKey, recordID = strings.TrimSpace(surfaceKey), strings.TrimSpace(bindingKey), strings.TrimSpace(recordID)
	if surfaceKey == "" && bindingKey == "" && recordID == "" {
		return profilebindingmodel.Reference{}, false, nil
	}
	candidates := []profilebindingmodel.Reference{}
	for _, profile := range profiles {
		if bindingKey != "" && profile.BindingKey != bindingKey || recordID != "" && profile.RecordID != recordID || surfaceKey != "" && !containsTrimmed(profile.SurfaceKeys, surfaceKey) {
			continue
		}
		candidates = append(candidates, profile)
	}
	if len(candidates) != 1 {
		return profilebindingmodel.Reference{}, false, businessPrincipalError(apperror.KindForbidden, "backend.identity.business_profile_selection_invalid", fmt.Errorf("selection resolved %d active profiles", len(candidates)))
	}
	return candidates[0], true, nil
}

func containsTrimmed(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func profileField(object definitionmodel.ObjectSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if field.Key == fieldKey {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func businessClaimType(fieldType string) string {
	switch fieldType {
	case "currency", "decimal", "number":
		return "decimal"
	case "integer":
		return "integer"
	case "boolean":
		return "boolean"
	case "date", "datetime", "relation":
		return fieldType
	default:
		return "text"
	}
}

func businessPrincipalError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}
