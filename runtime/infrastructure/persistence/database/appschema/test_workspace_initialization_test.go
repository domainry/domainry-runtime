package appschema

import (
	"os"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestMain(m *testing.M) {
	if err := principalmodel.ConfigureInstallationWorkspaceID("workspace-primary"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
