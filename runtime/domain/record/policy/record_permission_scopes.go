package policy

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

const (
	RecordOwnerUserIDSystemField = "owner_user_id"
	RecordOwnerOrgIDSystemField  = "owner_org_id"
)

func RecordOwnerFieldKey(_ definitionmodel.ObjectSchema) string {
	return RecordOwnerUserIDSystemField
}

func RecordOwnerOrgIDFieldKey(_ definitionmodel.ObjectSchema) string {
	return RecordOwnerOrgIDSystemField
}
