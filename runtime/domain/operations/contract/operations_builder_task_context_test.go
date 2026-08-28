package contract

import (
	"context"
	"testing"
)

func TestBuilderTaskIdentityIsLifecycleVerifiedAndNormalized(t *testing.T) {
	ctx := WithBuilderTaskID(context.Background(), " task-1 ")
	if got := BuilderTaskID(ctx); got != "task-1" {
		t.Fatalf("BuilderTaskID() = %q", got)
	}
	if WithBuilderTaskID(nil, "task") != nil || BuilderTaskID(nil) != "" {
		t.Fatal("nil caller context must remain nil")
	}
}
