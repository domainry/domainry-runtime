package report

import (
	"testing"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

func TestLifecycleExecutorIsReportOwned(t *testing.T) {
	executor := LifecycleExecutor(nil, nil, nil)
	if executor.Owner(t.Context()) != "report" {
		t.Fatalf("owner=%q", executor.Owner(t.Context()))
	}
	var _ lifecyclecontract.OwnerLifecycleExecutor = executor
}
