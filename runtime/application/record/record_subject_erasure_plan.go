package record

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"io/fs"
	"reflect"
	"strings"
	"time"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type recordSubjectErasurePlan struct {
	Version      int                                  `json:"version"`
	RequestID    string                               `json:"request_id"`
	WorkspaceID  string                               `json:"workspace_id"`
	SubjectID    string                               `json:"subject_id"`
	SchemaSHA256 string                               `json:"schema_sha256"`
	Mutations    []recordmodel.SubjectErasureMutation `json:"mutations"`
	Files        []recordSubjectErasureFile           `json:"files"`
}

type recordSubjectErasureFile struct {
	Reference lifecyclecontract.SubjectFileReference `json:"reference"`
	Evidence  lifecyclecontract.SubjectFileEvidence  `json:"evidence"`
	Retained  bool                                   `json:"retained_shared"`
}

func (s *RecordSubjectLifecycleApplicationService) PrepareSubjectErasure(ctx context.Context, requestID, workspaceID, subjectID string) (json.RawMessage, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(subjectID) == "" {
		return nil, fmt.Errorf("record subject erasure scope is required")
	}
	if _, ok := s.repository.(recordrepository.RecordSubjectErasureRepository); !ok {
		return nil, fmt.Errorf("record subject erasure transaction repository is unavailable")
	}
	matches, err := s.subjectRecords(ctx, workspaceID, subjectID)
	if err != nil {
		return nil, err
	}
	plan := recordSubjectErasurePlan{Version: 1, RequestID: requestID, WorkspaceID: workspaceID, SubjectID: subjectID, SchemaSHA256: s.subjectErasureSchemaHash(), Mutations: []recordmodel.SubjectErasureMutation{}, Files: []recordSubjectErasureFile{}}
	after := time.Now().UTC().Format(time.RFC3339Nano)
	seenFiles := map[string]bool{}
	for _, object := range s.objects {
		if err := recordpolicy.RecordValidateSubjectLifecycle(object); err != nil {
			return nil, err
		}
		if len(matches[object.Key]) > 0 {
			for _, field := range object.Fields {
				if field.DisabledAt != "" {
					continue
				}
				if _, declared := field.Config["lifecycle_erase"]; !declared {
					return nil, fmt.Errorf("subject erasure policy is required for %s.%s", object.Key, field.Key)
				}
			}
		}
		for _, record := range matches[object.Key] {
			mutation := recordmodel.SubjectErasureMutation{ObjectKey: object.Key, RecordID: record.ID, BeforeUpdatedAt: record.UpdatedAt, AfterUpdatedAt: after, Values: map[string]any{}}
			for _, field := range object.Fields {
				mode := recordpolicy.RecordSubjectEraseMode(field)
				value := record.Data[field.Key]
				if mode == recordpolicy.RecordLifecycleEraseRetain || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
					continue
				}
				if mode == recordpolicy.RecordLifecycleEraseAnonymize {
					mutation.Values[field.Key] = recordSubjectAnonymousValue(workspaceID, subjectID, object.Key, record.ID, field)
				} else {
					mutation.Values[field.Key] = nil
				}
				if !recordpolicy.RecordSubjectFileField(field) {
					continue
				}
				if s.files == nil {
					return nil, fmt.Errorf("record subject file store unavailable")
				}
				if _, ok := s.files.(lifecyclecontract.SubjectFileVersionDeleter); !ok {
					return nil, fmt.Errorf("record subject file version deletion unavailable")
				}
				for _, value := range recordSubjectFileValues(value) {
					if seenFiles[value] {
						continue
					}
					seenFiles[value] = true
					reference := lifecyclecontract.SubjectFileReference{WorkspaceID: workspaceID, ObjectKey: object.Key, RecordID: record.ID, FieldKey: field.Key, Reference: value}
					evidence, err := s.files.ExportSubjectFile(ctx, reference)
					if err != nil {
						return nil, err
					}
					if evidence.SHA256 == "" || evidence.Reference != value || value != "/uploads/"+evidence.Filename {
						return nil, fmt.Errorf("invalid subject file evidence")
					}
					evidence.Content = nil
					plan.Files = append(plan.Files, recordSubjectErasureFile{Reference: reference, Evidence: evidence})
				}
			}
			if len(mutation.Values) > 0 {
				plan.Mutations = append(plan.Mutations, mutation)
			}
		}
	}
	for index := range plan.Files {
		shared, err := s.subjectFileReferencedOutsidePlan(ctx, plan.WorkspaceID, plan.Files[index].Reference.Reference, plan.Mutations)
		if err != nil {
			return nil, err
		}
		plan.Files[index].Retained = shared
	}
	return json.Marshal(plan)
}

func (s *RecordSubjectLifecycleApplicationService) ErasePreparedSubject(ctx context.Context, requestID, workspaceID, subjectID string, raw json.RawMessage, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	if len(holds) > 0 {
		return nil, fmt.Errorf("record subject erasure blocked by legal hold")
	}
	var plan recordSubjectErasurePlan
	if s == nil || s.repository == nil || json.Unmarshal(raw, &plan) != nil || plan.Version != 1 || plan.RequestID != requestID || requestID == "" || plan.WorkspaceID != workspaceID || workspaceID == "" || plan.SubjectID != subjectID || subjectID == "" || plan.SchemaSHA256 != s.subjectErasureSchemaHash() {
		return nil, fmt.Errorf("record subject erasure plan scope or schema mismatch")
	}
	repository, ok := s.repository.(recordrepository.RecordSubjectErasureRepository)
	if !ok {
		return nil, fmt.Errorf("record subject erasure transaction repository is unavailable")
	}
	deleter, canDelete := s.files.(lifecyclecontract.SubjectFileVersionDeleter)
	if len(plan.Files) > 0 && !canDelete {
		return nil, fmt.Errorf("record subject file version deletion unavailable")
	}
	// Scope and sanitize every mutation again at the execution boundary. The
	// persisted plan must never become a general record-write capability.
	for _, mutation := range plan.Mutations {
		foundObject := false
		for _, object := range s.objects {
			if object.Key != mutation.ObjectKey {
				continue
			}
			foundObject = true
			for key, value := range mutation.Values {
				valid := false
				for _, field := range object.Fields {
					if field.Key != key {
						continue
					}
					switch recordpolicy.RecordSubjectEraseMode(field) {
					case recordpolicy.RecordLifecycleEraseDelete:
						valid = value == nil
					case recordpolicy.RecordLifecycleEraseAnonymize:
						valid = reflect.DeepEqual(value, recordSubjectAnonymousValue(workspaceID, subjectID, object.Key, mutation.RecordID, field))
					}
				}
				if !valid {
					return nil, fmt.Errorf("invalid subject erasure replacement")
				}
			}
		}
		if !foundObject {
			return nil, fmt.Errorf("unknown subject erasure object")
		}
	}
	for _, file := range plan.Files {
		if file.Reference.WorkspaceID != workspaceID || len(file.Evidence.Content) != 0 || file.Reference.Reference != file.Evidence.Reference {
			return nil, fmt.Errorf("invalid prepared file scope")
		}
		if !file.Retained {
			current, readErr := s.files.ExportSubjectFile(ctx, file.Reference)
			if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
				return nil, readErr
			}
			if readErr == nil && (current.SHA256 != file.Evidence.SHA256 || current.Size != file.Evidence.Size) {
				return nil, fmt.Errorf("subject file version changed after erasure planning")
			}
			shared, err := s.subjectFileReferencedOutsidePlan(ctx, workspaceID, file.Reference.Reference, plan.Mutations)
			if err != nil {
				return nil, err
			}
			if shared {
				return nil, fmt.Errorf("subject file acquired another reference after planning")
			}
		}
	}
	if err := repository.ApplySubjectErasure(ctx, workspaceID, s.objects, plan.Mutations); err != nil {
		return nil, err
	}
	deleted, retained := []lifecyclecontract.SubjectFileEvidence{}, []lifecyclecontract.SubjectFileEvidence{}
	for _, file := range plan.Files {
		if file.Retained {
			retained = append(retained, file.Evidence)
			continue
		}
		// Recheck after detaching business fields so another live reference can
		// never be treated as an orphan merely because it was absent in the plan.
		shared, err := s.subjectFileReferencedOutsidePlan(ctx, workspaceID, file.Reference.Reference, nil)
		if err != nil {
			return nil, err
		}
		if shared {
			return nil, fmt.Errorf("subject file still has a live reference")
		}
		evidence, err := deleter.DeleteSubjectFileVersion(ctx, file.Reference, file.Evidence)
		if err != nil {
			return nil, err
		}
		deleted = append(deleted, evidence)
	}
	return json.Marshal(map[string]any{"updated_records": len(plan.Mutations), "deleted_files": deleted, "retained_shared_files": retained})
}

func (s *RecordSubjectLifecycleApplicationService) subjectErasureSchemaHash() string {
	raw, _ := json.Marshal(struct {
		Objects               []definitionmodel.ObjectSchema
		ProfileIdentityFields map[string][]string
	}{s.objects, s.profileIdentityFields})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (s *RecordSubjectLifecycleApplicationService) SubjectRecordReferences(ctx context.Context, workspaceID, subjectID string) ([]recordmodel.SubjectRecordReference, error) {
	matches, err := s.subjectRecords(ctx, workspaceID, subjectID)
	if err != nil {
		return nil, err
	}
	result := []recordmodel.SubjectRecordReference{}
	for _, object := range s.objects {
		for _, record := range matches[object.Key] {
			result = append(result, recordmodel.SubjectRecordReference{ObjectKey: object.Key, RecordID: record.ID})
		}
	}
	return result, nil
}

func (s *RecordSubjectLifecycleApplicationService) subjectFileReferencedOutsidePlan(ctx context.Context, workspaceID, reference string, mutations []recordmodel.SubjectErasureMutation) (bool, error) {
	ignored := map[string]map[string]any{}
	for _, mutation := range mutations {
		ignored[mutation.ObjectKey+"\x00"+mutation.RecordID] = mutation.Values
	}
	for _, object := range s.objects {
		after := ""
		for {
			page, err := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: recordSubjectPageSize, SkipTotal: true, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, AfterID: after, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}})
			if err != nil {
				return false, err
			}
			for _, record := range page.Items {
				for key, value := range record.Data {
					if _, removed := ignored[object.Key+"\x00"+record.ID][key]; removed {
						continue
					}
					if recordSubjectValueContainsReference(value, reference) {
						return true, nil
					}
				}
			}
			if !page.HasNext {
				break
			}
			if len(page.Items) == 0 || page.Items[len(page.Items)-1].ID == after {
				return false, fmt.Errorf("subject reference scan did not advance")
			}
			after = page.Items[len(page.Items)-1].ID
		}
	}
	return false, nil
}

func recordSubjectValueContainsReference(value any, reference string) bool {
	switch typed := value.(type) {
	case string:
		return typed == reference
	case []string:
		for _, item := range typed {
			if item == reference {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if recordSubjectValueContainsReference(item, reference) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if recordSubjectValueContainsReference(item, reference) {
				return true
			}
		}
	}
	return false
}

var _ lifecyclecontract.PreparedSubjectErasureHandler = (*RecordSubjectLifecycleApplicationService)(nil)
