package auditbinding

import (
	"context"

	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// AuditRecordAccessApplicationService is Runtime's narrow cross-owner adapter
// for Audit. Audit owns query policy; Runtime owns record authorization
// and field-level projection.
type AuditRecordAccessApplicationService struct {
	records *recordapplication.RecordApplicationService
	objects func() []definitionmodel.ObjectSchema
}

func NewAuditRecordAccessApplicationService(records *recordapplication.RecordApplicationService, objects func() []definitionmodel.ObjectSchema) *AuditRecordAccessApplicationService {
	return &AuditRecordAccessApplicationService{records: records, objects: objects}
}

func (s *AuditRecordAccessApplicationService) AuthorizeAuditRecord(ctx context.Context, principal principalmodel.Principal, objectKey, recordID string) error {
	_, err := s.records.GetRecord(ctx, objectKey, recordID, principal)
	return err
}

func (s *AuditRecordAccessApplicationService) ProjectAuditEvents(ctx context.Context, events []auditcontract.Event, principal principalmodel.Principal) ([]auditcontract.Event, error) {
	objects := map[string]definitionmodel.ObjectSchema{}
	for _, object := range s.objects() {
		objects[object.Key] = object
	}
	projected := append([]auditcontract.Event(nil), events...)
	for index := range projected {
		object, exists := objects[projected[index].ObjectKey]
		if !exists || projected[index].RecordID == "" {
			continue
		}
		if projected[index].Before != nil {
			values, err := s.records.ProjectRecordFields(ctx, principal, object, []recordmodel.Record{{ID: projected[index].RecordID, Data: projected[index].Before}}, "audit")
			if err != nil {
				return nil, err
			}
			projected[index].Before = values[0].Data
		}
		if projected[index].After != nil {
			values, err := s.records.ProjectRecordFields(ctx, principal, object, []recordmodel.Record{{ID: projected[index].RecordID, Data: projected[index].After}}, "audit")
			if err != nil {
				return nil, err
			}
			projected[index].After = values[0].Data
		}
	}
	return projected, nil
}
