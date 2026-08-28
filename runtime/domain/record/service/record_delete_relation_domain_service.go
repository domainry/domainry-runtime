package service

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	"context"
	"sort"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordDeleteReference struct {
	Object definitionmodel.ObjectSchema
	Field  definitionmodel.FieldSchema
	Record recordmodel.Record
}

type RecordDeleteRelationCallbacks struct {
	SetNull func(context.Context, RecordDeleteReference) error
	Cascade func(context.Context, RecordDeleteReference) error
}

// RecordDeleteRelationDomainService removes record relations.
type RecordDeleteRelationDomainService struct {
	repository recordrepository.RecordRepository
	schemaMap  func() map[string]definitionmodel.ObjectSchema
}

func NewRecordDeleteRelationDomainService(repository recordrepository.RecordRepository, schemaMap func() map[string]definitionmodel.ObjectSchema) *RecordDeleteRelationDomainService {
	return &RecordDeleteRelationDomainService{repository: repository, schemaMap: schemaMap}
}

func (s *RecordDeleteRelationDomainService) Apply(ctx context.Context, workspaceID, targetObjectKey, targetRecordID string, callbacks RecordDeleteRelationCallbacks) error {
	references, err := s.References(ctx, workspaceID, targetObjectKey, targetRecordID)
	if err != nil {
		return err
	}
	for _, reference := range references {
		if recordpolicy.RecordRelationDeletePolicy(reference.Field) == "restrict" {
			return recordServiceError(
				apperror.KindConflict,
				"backend.relation.delete_restricted",
				nil,
				"object", targetObjectKey,
				"record", targetRecordID,
				"source_object", reference.Object.Key,
				"field", reference.Field.Key,
			)
		}
	}
	for _, reference := range references {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch recordpolicy.RecordRelationDeletePolicy(reference.Field) {
		case "set_null":
			if callbacks.SetNull != nil {
				if err := callbacks.SetNull(ctx, reference); err != nil {
					return err
				}
			}
		case "cascade":
			if callbacks.Cascade != nil {
				if err := callbacks.Cascade(ctx, reference); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *RecordDeleteRelationDomainService) References(ctx context.Context, workspaceID, targetObjectKey, targetRecordID string) ([]RecordDeleteReference, error) {
	schema := s.schemaMap()
	keys := make([]string, 0, len(schema))
	for key := range schema {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	references := []RecordDeleteReference{}
	for _, key := range keys {
		object := schema[key]
		for _, field := range object.Fields {
			if field.Type != "relation" || relationTarget(field) != targetObjectKey {
				continue
			}
			for pageNumber := 1; ; pageNumber++ {
				page, err := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{
					Page:     pageNumber,
					PageSize: 200,
					Filters:  map[string]any{field.Key: targetRecordID},
				})
				if err != nil {
					return nil, recordInternalError("list relation delete references", err)
				}
				for _, record := range page.Items {
					references = append(references, RecordDeleteReference{Object: object, Field: field, Record: record})
				}
				if !page.HasNext {
					break
				}
			}
		}
	}
	return references, nil
}
