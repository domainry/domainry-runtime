package scheduler

import (
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type schedulerFixedClock struct{ now time.Time }

func (c schedulerFixedClock) Now() time.Time { return c.now }

func schedulerTestPrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "operator-1", WorkspaceID: "workspace-a",
	}}, accessfixture.Bundle{Permissions: permissions, RecordScope: "all_records"})
}
