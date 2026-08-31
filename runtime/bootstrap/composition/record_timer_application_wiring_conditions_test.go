package composition

import (
	"context"
	"testing"

	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRecordTimerTargetRuntimeAdapterConditions(t *testing.T) {
	adapter := recordTimerTargetRuntimeAdapter{}
	if err := adapter.ExecuteRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{}, principalmodel.Principal{}); err == nil {
		t.Fatal("missing timer runtime accepted")
	}
	called := false
	adapter.execute = func(context.Context, recordtimerapplication.RecordTimerExecution, principalmodel.Principal) error {
		called = true
		return nil
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{}, principalmodel.Principal{}); err != nil || !called {
		t.Fatalf("timer called=%v err=%v", called, err)
	}
}
