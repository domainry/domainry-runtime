package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type RecordDomainServiceDependencies struct {
	Repository        recordrepository.RecordRepository
	Reader            *RecordReadDomainService
	References        *RecordReferenceDomainService
	IdentityDirectory identitysdk.Directory
}

// RecordDomainService is the canonical Record owner boundary. Cross-domain
// policies are supplied while its dependencies are assembled; callers depend on
// this service instead of a process-level service aggregate.
// RecordDomainService owns record lifecycle behavior.
type RecordDomainService struct {
	repository        recordrepository.RecordRepository
	reader            *RecordReadDomainService
	references        *RecordReferenceDomainService
	identityDirectory identitysdk.Directory
}

func NewRecordDomainService(dependencies RecordDomainServiceDependencies) *RecordDomainService {
	return &RecordDomainService{
		repository: dependencies.Repository, reader: dependencies.Reader, references: dependencies.References,
		identityDirectory: dependencies.IdentityDirectory,
	}
}

func (s *RecordDomainService) Repository() recordrepository.RecordRepository {
	if s == nil {
		return nil
	}
	return s.repository
}

func (s *RecordDomainService) IdentityDirectory() identitysdk.Directory {
	if s == nil {
		return nil
	}
	return s.identityDirectory
}

func (s *RecordDomainService) ListRecords(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.reader.ListRecords(ctx, objectKey, query, principal)
}

func (s *RecordDomainService) ListRecordsForAction(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.reader.ListRecordsForAction(ctx, objectKey, query, principal)
}

func (s *RecordDomainService) GetRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.reader.GetRecord(ctx, objectKey, recordID, principal)
}

func (s *RecordDomainService) GetRecordForUpdate(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.reader.GetRecordForUpdate(ctx, objectKey, recordID, principal)
}

func (s *RecordDomainService) GetRecordForAction(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.reader.GetRecordForAction(ctx, objectKey, recordID, principal)
}

func (s *RecordDomainService) GetRecordForUpdateForAction(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.reader.GetRecordForUpdateForAction(ctx, objectKey, recordID, principal)
}

func (s *RecordDomainService) RecordReferences(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (RecordReferenceSummary, error) {
	return s.references.References(ctx, objectKey, recordID, principal)
}

func (s *RecordDomainService) RelatedRecords(ctx context.Context, objectKey, recordID, relatedObjectKey string, request RecordRelatedRecordsRequest, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.references.Related(ctx, objectKey, recordID, relatedObjectKey, request, principal)
}

func (s *RecordDomainService) IdentityProfileReferences(ctx context.Context, userID string, principal principalmodel.Principal) ([]RecordIdentityProfileReference, error) {
	return s.reader.IdentityProfileReferences(ctx, userID, principal)
}
