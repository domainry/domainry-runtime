package validation

import (
	"context"
	"errors"
	"testing"
)

func TestSchedulerValidateScheduleFragmentHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := SchedulerValidateScheduleFragment(ctx, map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
