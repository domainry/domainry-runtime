package record

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"io/fs"
	"sort"
	"strings"

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
	subjectUploads        func(context.Context, string, string) ([]lifecyclecontract.SubjectFileReference, error)
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

func (s *RecordSubjectLifecycleApplicationService) BindSubjectUploads(resolve func(context.Context, string, string) ([]lifecyclecontract.SubjectFileReference, error)) {
	s.subjectUploads = resolve
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
	fileReferences := map[string]bool{}
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
				for _, reference := range recordSubjectFileValues(record.Data[field.Key]) {
					fileReferences[reference] = true
				}
			}
		}
	}
	if s.subjectUploads != nil {
		refs, err := s.subjectUploads(ctx, workspaceID, resolvedIdentity)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			if ref.WorkspaceID != workspaceID {
				return nil, fmt.Errorf("upload subject workspace mismatch")
			}
			fileReferences[ref.Reference] = true
		}
	}
	return json.Marshal(map[string]any{"objects": objectCounts, "files": len(fileReferences)})
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
	if s.subjectUploads != nil {
		refs, err := s.subjectUploads(ctx, workspaceID, resolvedIdentity)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, evidence := range export.Files {
			seen[evidence.Reference] = true
		}
		for _, ref := range refs {
			if ref.WorkspaceID != workspaceID {
				return nil, fmt.Errorf("upload subject workspace mismatch")
			}
			if seen[ref.Reference] {
				continue
			}
			if s.files == nil {
				return nil, fmt.Errorf("record subject file store unavailable")
			}
			evidence, err := s.files.ExportSubjectFile(ctx, ref)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			seen[ref.Reference] = true
			export.Files = append(export.Files, evidence)
		}
	}
	return json.Marshal(export)
}

func (s *RecordSubjectLifecycleApplicationService) ExportSubjectForRequest(ctx context.Context, _ string, workspaceID, resolvedIdentity string) (json.RawMessage, error) {
	return s.ExportSubject(ctx, workspaceID, resolvedIdentity)
}

// Record erasure must run through Lifecycle, which durably saves the plan
// before detaching file references. Direct execution cannot guarantee retry.
func (s *RecordSubjectLifecycleApplicationService) EraseSubject(context.Context, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return nil, fmt.Errorf("record subject erasure requires a persisted Lifecycle plan")
}
func (s *RecordSubjectLifecycleApplicationService) EraseSubjectForRequest(context.Context, string, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return nil, fmt.Errorf("record subject erasure requires a persisted Lifecycle plan")
}

func (s *RecordSubjectLifecycleApplicationService) subjectRecords(ctx context.Context, workspaceID, resolvedIdentity string) (map[string][]recordmodel.Record, error) {
	result := map[string][]recordmodel.Record{}
	if s == nil || s.repository == nil {
		return result, fmt.Errorf("record subject repository unavailable")
	}
	byObject := map[string]map[string]recordmodel.Record{}
	for _, object := range s.objects {
		byObject[object.Key] = map[string]recordmodel.Record{}
	}
	visited := map[string]bool{}
	total := 0
	addMatches := func(object definitionmodel.ObjectSchema, fieldKey, subjectValue string) error {
		key := object.Key + "\x00" + fieldKey + "\x00" + subjectValue
		if visited[key] {
			return nil
		}
		visited[key] = true
		afterID := ""
		for {
			page, err := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: recordSubjectPageSize, SkipTotal: true, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, AfterID: afterID, Filters: map[string]any{fieldKey: subjectValue}, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}})
			if err != nil {
				return err
			}
			for _, record := range page.Items {
				if _, known := byObject[object.Key][record.ID]; !known {
					total++
					if total > 10000 {
						return fmt.Errorf("subject record limit exceeded")
					}
					byObject[object.Key][record.ID] = record
				}
			}
			if !page.HasNext {
				break
			}
			if len(page.Items) == 0 || page.Items[len(page.Items)-1].ID <= afterID {
				return fmt.Errorf("subject record cursor did not advance")
			}
			afterID = page.Items[len(page.Items)-1].ID
		}
		return nil
	}
	for _, object := range s.objects {
		keys := append([]string(nil), s.profileIdentityFields[object.Key]...)
		for _, field := range recordpolicy.RecordSubjectIdentityFields(object) {
			keys = appendUniqueRecordSubjectField(keys, field.Key)
		}
		for _, key := range keys {
			if err := addMatches(object, key, resolvedIdentity); err != nil {
				return nil, err
			}
		}
	}
	// Follow only declared subject-ownership relations. Ordinary references to
	// shared business entities do not make their records owned by this subject.
	for {
		before := total
		for _, object := range s.objects {
			for _, field := range object.Fields {
				target := recordpolicy.RecordSubjectRelationTarget(field)
				if target == "" {
					continue
				}
				parents, known := byObject[target]
				if !known {
					return nil, fmt.Errorf("subject relation target %s is unavailable", target)
				}
				ids := make([]string, 0, len(parents))
				for id := range parents {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				for _, id := range ids {
					if err := addMatches(object, field.Key, id); err != nil {
						return nil, err
					}
				}
			}
		}
		if before == total {
			break
		}
	}
	for _, object := range s.objects {
		ids := make([]string, 0, len(byObject[object.Key]))
		for id := range byObject[object.Key] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			result[object.Key] = append(result[object.Key], byObject[object.Key][id])
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
