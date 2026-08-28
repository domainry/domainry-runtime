package record

import (
	"context"
	"errors"
	"testing"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type recordBatchRuntimeStore struct {
	*recordBatchStoreProbe
	getErr          error
	heartbeatErr    error
	getJob          *recordmodel.RecordBatchJob
	getCalled       chan struct{}
	heartbeatCalled chan struct{}
}

func (s *recordBatchRuntimeStore) GetRecordBatchJob(ctx context.Context, workspaceID, id string) (recordmodel.RecordBatchJob, bool, error) {
	if s.getCalled != nil {
		select {
		case s.getCalled <- struct{}{}:
		default:
		}
	}
	if s.getErr != nil {
		return recordmodel.RecordBatchJob{}, false, s.getErr
	}
	if s.getJob != nil {
		return *s.getJob, true, nil
	}
	return s.recordBatchStoreProbe.GetRecordBatchJob(ctx, workspaceID, id)
}

func (s *recordBatchRuntimeStore) HeartbeatRecordBatchJob(context.Context, recordmodel.RecordBatchJob, time.Duration, time.Time) error {
	if s.heartbeatCalled != nil {
		select {
		case s.heartbeatCalled <- struct{}{}:
		default:
		}
	}
	return s.heartbeatErr
}

func TestRecordBatchCancellationPollOutcomes(t *testing.T) {
	job := recordmodel.RecordBatchJob{ID: "job-1", WorkspaceID: "workspace-a"}
	for _, test := range []struct {
		name      string
		store     *recordBatchRuntimeStore
		cancelled bool
	}{
		{name: "get error", store: &recordBatchRuntimeStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, getErr: errors.New("get failed")}},
		{name: "not found", store: &recordBatchRuntimeStore{recordBatchStoreProbe: &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{}}}},
		{name: "still running", store: &recordBatchRuntimeStore{recordBatchStoreProbe: &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{"job-1": {ID: "job-1", WorkspaceID: "workspace-a", Status: "running"}}}}},
		{name: "cancelled", store: &recordBatchRuntimeStore{recordBatchStoreProbe: &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{"job-1": {ID: "job-1", WorkspaceID: "workspace-a", Status: "cancelled"}}}}, cancelled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := make(chan struct{}, 1)
			test.store.getCalled = called
			parent, parentCancel := context.WithCancel(t.Context())
			defer parentCancel()
			service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: test.store})
			ctx, cleanup := service.cancellationContextWithIntervals(parent, job, time.Millisecond, time.Hour)
			defer cleanup()
			select {
			case <-called:
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation poll did not run")
			}
			if test.cancelled {
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled job did not cancel context")
				}
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatalf("ctx err=%v", ctx.Err())
				}
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("poll outcome unexpectedly cancelled context: %v", ctx.Err())
			default:
			}
		})
	}
}

func TestRecordBatchHeartbeatOutcomes(t *testing.T) {
	job := recordmodel.RecordBatchJob{ID: "job-1", WorkspaceID: "workspace-a"}
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "failure", err: errors.New("heartbeat failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := make(chan struct{}, 1)
			parent, parentCancel := context.WithCancel(t.Context())
			defer parentCancel()
			store := &recordBatchRuntimeStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, heartbeatErr: test.err, heartbeatCalled: called}
			service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
			ctx, cleanup := service.cancellationContextWithIntervals(parent, job, time.Hour, time.Millisecond)
			defer cleanup()
			select {
			case <-called:
			case <-time.After(5 * time.Second):
				t.Fatal("heartbeat did not run")
			}
			if test.err != nil {
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("heartbeat failure did not cancel context")
				}
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatalf("ctx err=%v", ctx.Err())
				}
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("successful heartbeat unexpectedly cancelled context: %v", ctx.Err())
			default:
			}
		})
	}
}
