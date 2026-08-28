package record

import (
	"context"
	"fmt"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type RecordOwnerDepartmentPathApplicationService struct {
	repository     recordrepository.RecordRepository
	schemaMap      func() map[string]definitionmodel.ObjectSchema
	updateInternal RecordInternalUpdater
}

func NewRecordOwnerDepartmentPathApplicationService(repository recordrepository.RecordRepository, schemaMap func() map[string]definitionmodel.ObjectSchema, updateInternal RecordInternalUpdater) *RecordOwnerDepartmentPathApplicationService {
	return &RecordOwnerDepartmentPathApplicationService{repository: repository, schemaMap: schemaMap, updateInternal: updateInternal}
}

func (s *RecordOwnerDepartmentPathApplicationService) Rebuild(ctx context.Context, workspaceID string, workforce []identitysdk.WorkforceEntry) (int, error) {
	if _, err := principalmodel.NewWorkspaceCommandScope(workspaceID); err != nil {
		return 0, err
	}
	usersByID := map[string]identitysdk.WorkforceEntry{}
	for _, entry := range workforce {
		if strings.TrimSpace(entry.IdentityUserID) != "" {
			usersByID[entry.IdentityUserID] = entry
		}
	}
	if len(usersByID) == 0 {
		return 0, nil
	}
	schema := s.schemaMap()
	objects := make([]definitionmodel.ObjectSchema, 0, len(schema))
	for _, object := range schema {
		objects = append(objects, object)
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	updated := 0
	for _, object := range objects {
		ownerField := recordpolicy.RecordOwnerFieldKey(object)
		departmentIDField := recordpolicy.RecordOwnerDepartmentIDFieldKey(object)
		departmentPathField := recordpolicy.RecordOwnerDepartmentPathFieldKey(object)
		if ownerField == "" || departmentIDField == "" || departmentPathField == "" {
			continue
		}
		afterID := ""
		for {
			result, err := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 200, SkipTotal: true, AfterID: afterID, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}})
			if err != nil {
				return updated, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": "list records for owner department rebuild"}, Err: err}
			}
			for _, record := range result.Items {
				ownerID := recordStringValue(record, ownerField)
				entry, ok := usersByID[ownerID]
				if !ok || strings.TrimSpace(entry.OrganizationUnitID) == "" || strings.TrimSpace(entry.OrganizationPath) == "" {
					continue
				}
				if recordStringValue(record, departmentIDField) == entry.OrganizationUnitID && recordStringValue(record, departmentPathField) == entry.OrganizationPath {
					continue
				}
				record.Data[departmentIDField] = entry.OrganizationUnitID
				record.Data[departmentPathField] = entry.OrganizationPath
				record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				if err := s.updateInternal(ctx, workspaceID, object, record, "rebuild owner department path"); err != nil {
					return updated, err
				}
				updated++
			}
			if !result.HasNext {
				break
			}
			afterID = result.Items[len(result.Items)-1].ID
		}
	}
	return updated, nil
}

func recordStringValue(record recordmodel.Record, fieldKey string) string {
	value, ok := record.Data[fieldKey]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
