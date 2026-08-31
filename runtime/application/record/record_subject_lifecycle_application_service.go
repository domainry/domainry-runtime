package record

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"sort"
	"strings"
	"time"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

const recordSubjectPageSize = 200

type RecordSubjectLifecycleApplicationService struct {
	repository            recordrepository.RecordRepository
	objects               []definitionmodel.ObjectSchema
	files                 lifecyclecontract.SubjectFileStore
	profileIdentityFields map[string][]string
}

type recordSubjectExport struct {
	Objects []recordSubjectObjectExport             `json:"objects"`
	Files   []lifecyclecontract.SubjectFileEvidence `json:"files"`
}

type recordSubjectObjectExport struct {
	ObjectKey string               `json:"object_key"`
	Records   []recordmodel.Record `json:"records"`
}

func NewRecordSubjectLifecycleApplicationService(repository recordrepository.RecordRepository, objects []definitionmodel.ObjectSchema, files lifecyclecontract.SubjectFileStore, extensionSets ...[]profilebindingmodel.Binding) *RecordSubjectLifecycleApplicationService {
	service := &RecordSubjectLifecycleApplicationService{
		repository: repository, objects: append([]definitionmodel.ObjectSchema(nil), objects...), files: files,
		profileIdentityFields: map[string][]string{},
	}
	for _, extensions := range extensionSets {
		for _, extension := range extensions {
			objectKey, fieldKey := strings.TrimSpace(extension.ObjectKey), strings.TrimSpace(extension.IdentityRelationField)
			if objectKey == "" || fieldKey == "" {
				continue
			}
			service.profileIdentityFields[objectKey] = appendUniqueRecordSubjectField(service.profileIdentityFields[objectKey], fieldKey)
		}
	}
	return service
}

func (s *RecordSubjectLifecycleApplicationService) Owner(context.Context) string { return "record" }

func (s *RecordSubjectLifecycleApplicationService) ResolveSubject(_ context.Context, workspaceID, subjectType, subjectID string) (string, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(subjectType) == "" || strings.TrimSpace(subjectID) == "" {
		return "", fmt.Errorf("record subject workspace, type, and identity are required")
	}
	return strings.TrimSpace(subjectID), nil
}

func (s *RecordSubjectLifecycleApplicationService) PreviewSubject(ctx context.Context, workspaceID, resolvedIdentity string) (json.RawMessage, error) {
	matches, err := s.subjectRecords(ctx, workspaceID, resolvedIdentity)
	if err != nil {
		return nil, err
	}
	objectCounts := map[string]int{}
	fileCount := 0
	for _, object := range s.objects {
		records := matches[object.Key]
		if len(records) == 0 {
			continue
		}
		objectCounts[object.Key] = len(records)
		for _, field := range object.Fields {
			if !recordpolicy.RecordSubjectFileField(field) {
				continue
			}
			for _, record := range records {
				fileCount += len(recordSubjectFileValues(record.Data[field.Key]))
			}
		}
	}
	return json.Marshal(map[string]any{"objects": objectCounts, "files": fileCount})
}

func (s *RecordSubjectLifecycleApplicationService) ExportSubject(ctx context.Context, workspaceID, resolvedIdentity string) (json.RawMessage, error) {
	matches, err := s.subjectRecords(ctx, workspaceID, resolvedIdentity)
	if err != nil {
		return nil, err
	}
	export := recordSubjectExport{Objects: []recordSubjectObjectExport{}, Files: []lifecyclecontract.SubjectFileEvidence{}}
	for _, object := range s.objects {
		records := matches[object.Key]
		if len(records) == 0 {
			continue
		}
		export.Objects = append(export.Objects, recordSubjectObjectExport{ObjectKey: object.Key, Records: records})
		files, fileErr := s.exportRecordFiles(ctx, workspaceID, object, records)
		if fileErr != nil {
			return nil, fileErr
		}
		export.Files = append(export.Files, files...)
	}
	return json.Marshal(export)
}

func (s *RecordSubjectLifecycleApplicationService) ExportSubjectForRequest(ctx context.Context, _ string, workspaceID, resolvedIdentity string) (json.RawMessage, error) {
	return s.ExportSubject(ctx, workspaceID, resolvedIdentity)
}

func (s *RecordSubjectLifecycleApplicationService) EraseSubject(ctx context.Context, workspaceID, resolvedIdentity string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	if len(holds) > 0 {
		return nil, fmt.Errorf("record subject erasure blocked by legal hold")
	}
	matches, err := s.subjectRecords(ctx, workspaceID, resolvedIdentity)
	if err != nil {
		return nil, err
	}
	evidence := map[string]any{"updated_records": 0, "deleted_files": []lifecyclecontract.SubjectFileEvidence{}}
	deletedFiles := []lifecyclecontract.SubjectFileEvidence{}
	updated := 0
	for _, object := range s.objects {
		for _, record := range matches[object.Key] {
			changed := false
			filesToDelete := []lifecyclecontract.SubjectFileReference{}
			for _, field := range object.Fields {
				mode := recordpolicy.RecordSubjectEraseMode(field)
				if mode == recordpolicy.RecordLifecycleEraseRetain {
					continue
				}
				value, present := record.Data[field.Key]
				if !present || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
					continue
				}
				if recordpolicy.RecordSubjectFileField(field) && mode == recordpolicy.RecordLifecycleEraseDelete {
					for _, reference := range recordSubjectFileValues(value) {
						filesToDelete = append(filesToDelete, lifecyclecontract.SubjectFileReference{WorkspaceID: workspaceID, ObjectKey: object.Key, RecordID: record.ID, FieldKey: field.Key, Reference: reference})
					}
					record.Data[field.Key] = nil
					changed = true
					continue
				}
				switch mode {
				case recordpolicy.RecordLifecycleEraseDelete:
					record.Data[field.Key] = nil
				case recordpolicy.RecordLifecycleEraseAnonymize:
					record.Data[field.Key] = recordSubjectAnonymousValue(workspaceID, resolvedIdentity, object.Key, record.ID, field)
				}
				changed = true
			}
			if !changed {
				continue
			}
			record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := s.repository.UpdateRecord(ctx, workspaceID, object, record); err != nil {
				return nil, err
			}
			updated++
			for _, reference := range filesToDelete {
				if s.files == nil {
					return nil, fmt.Errorf("record subject file store unavailable")
				}
				fileEvidence, deleteErr := s.files.DeleteSubjectFile(ctx, reference)
				if deleteErr != nil {
					return nil, deleteErr
				}
				deletedFiles = append(deletedFiles, fileEvidence)
			}
		}
	}
	evidence["updated_records"], evidence["deleted_files"] = updated, deletedFiles
	return json.Marshal(evidence)
}

func (s *RecordSubjectLifecycleApplicationService) EraseSubjectForRequest(ctx context.Context, _ string, workspaceID, resolvedIdentity string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return s.EraseSubject(ctx, workspaceID, resolvedIdentity, holds)
}

func (s *RecordSubjectLifecycleApplicationService) subjectRecords(ctx context.Context, workspaceID, resolvedIdentity string) (map[string][]recordmodel.Record, error) {
	result := map[string][]recordmodel.Record{}
	if s == nil || s.repository == nil {
		return result, fmt.Errorf("record subject repository unavailable")
	}
	for _, object := range s.objects {
		identityFields := recordpolicy.RecordSubjectIdentityFields(object)
		identityFieldKeys := make([]string, 0, len(identityFields)+len(s.profileIdentityFields[object.Key]))
		for _, field := range identityFields {
			identityFieldKeys = appendUniqueRecordSubjectField(identityFieldKeys, field.Key)
		}
		for _, field := range s.profileIdentityFields[object.Key] {
			identityFieldKeys = appendUniqueRecordSubjectField(identityFieldKeys, field)
		}
		if len(identityFieldKeys) == 0 {
			continue
		}
		byID := map[string]recordmodel.Record{}
		for _, fieldKey := range identityFieldKeys {
			afterID := ""
			for {
				values, err := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: recordSubjectPageSize, SkipTotal: true, AfterID: afterID, Filters: map[string]any{fieldKey: resolvedIdentity}, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}})
				if err != nil {
					return nil, err
				}
				for _, record := range values.Items {
					byID[record.ID] = record
				}
				if !values.HasNext {
					break
				}
				afterID = values.Items[len(values.Items)-1].ID
			}
		}
		ids := make([]string, 0, len(byID))
		for id := range byID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			result[object.Key] = append(result[object.Key], byID[id])
		}
	}
	return result, nil
}

func appendUniqueRecordSubjectField(fields []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return fields
	}
	for _, field := range fields {
		if field == candidate {
			return fields
		}
	}
	return append(fields, candidate)
}

func (s *RecordSubjectLifecycleApplicationService) exportRecordFiles(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, records []recordmodel.Record) ([]lifecyclecontract.SubjectFileEvidence, error) {
	result := []lifecyclecontract.SubjectFileEvidence{}
	for _, field := range object.Fields {
		if !recordpolicy.RecordSubjectFileField(field) {
			continue
		}
		if s.files == nil {
			return nil, fmt.Errorf("record subject file store unavailable")
		}
		for _, record := range records {
			for _, value := range recordSubjectFileValues(record.Data[field.Key]) {
				evidence, err := s.files.ExportSubjectFile(ctx, lifecyclecontract.SubjectFileReference{WorkspaceID: workspaceID, ObjectKey: object.Key, RecordID: record.ID, FieldKey: field.Key, Reference: value})
				if err != nil {
					return nil, err
				}
				result = append(result, evidence)
			}
		}
	}
	return result, nil
}

func recordSubjectFileValues(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			result = append(result, strings.TrimSpace(typed))
		}
	case []string:
		for _, item := range typed {
			result = append(result, recordSubjectFileValues(item)...)
		}
	case []any:
		for _, item := range typed {
			result = append(result, recordSubjectFileValues(item)...)
		}
	}
	return result
}

func recordSubjectAnonymousValue(workspaceID, identity, objectKey, recordID string, field definitionmodel.FieldSchema) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{workspaceID, identity, objectKey, recordID, field.Key}, "\x00")))
	token := hex.EncodeToString(digest[:12])
	if strings.EqualFold(strings.TrimSpace(field.Type), "email") {
		return "erased+" + token + "@invalid.local"
	}
	return "erased-" + token
}

var _ lifecyclecontract.SubjectExecutionHandler = (*RecordSubjectLifecycleApplicationService)(nil)
