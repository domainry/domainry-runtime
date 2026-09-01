package appschema

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestInstalledActionAuthorizationRequiresPersistedActionContract(t *testing.T) {
	installed := manifestmodel.ManifestSchema{
		Actions: []definitionmodel.ActionSchema{{
			Key: "booking.cancel",
		}},
	}
	persisted := installed
	if err := validateInstalledActionAuthorization(installed, persisted); err != nil {
		t.Fatalf("matching installed authorization was rejected: %v", err)
	}

	persisted.Actions = nil
	if err := validateInstalledActionAuthorization(installed, persisted); err == nil ||
		!strings.Contains(err.Error(), `installed Action "booking.cancel" was not persisted`) {
		t.Fatalf("missing persisted Action was not rejected: %v", err)
	}

}
