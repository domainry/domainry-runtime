package policy

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func ActionIsObjectKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case definitionmodel.ActionKindObjectCreate, definitionmodel.ActionKindObjectOperation, definitionmodel.ActionKindBulkOperation:
		return true
	default:
		return false
	}
}

func ActionIsRecordKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case definitionmodel.ActionKindRecordUpdate, definitionmodel.ActionKindRecordDelete, definitionmodel.ActionKindRecordRestore, definitionmodel.ActionKindTransitionState, definitionmodel.ActionKindConditionalUpdate, definitionmodel.ActionKindRecordOperation:
		return true
	default:
		return false
	}
}
