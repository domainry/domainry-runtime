package validation

import (
	"strings"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func MetadataRollbackRequestErrorCode(request metadatamodel.MetadataDefinitionRollbackRequest) string {
	if strings.TrimSpace(request.TargetVersion) == "" || strings.TrimSpace(request.ExpectedSchemaHash) == "" || strings.TrimSpace(request.BusinessReason) == "" || strings.TrimSpace(request.ChangePlanID) == "" || strings.TrimSpace(request.AuthoringContractVersion) == "" || strings.TrimSpace(request.AuthoringContractHash) == "" {
		return "backend.metadata.rollback_contract_required"
	}
	return ""
}
