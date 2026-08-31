package runtime

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchedulerWorkerHasNoRuntimeOwnedExecutionFallback(t *testing.T) {
	source, err := os.ReadFile("worker_lifecycle.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{"Applications().Scheduler.StartWorker", "schedulerapplication.WorkerConfig"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Runtime Scheduler worker still contains legacy execution fallback %q", forbidden)
		}
	}
}

func TestRuntimeProductionDoesNotReintroduceSchedulerOwnedLifecycle(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	forbidden := []string{`"scheduler_cursor"`, `"job_run"`, `"job_run_event"`, `"job_dead_letter"`}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, value := range forbidden {
			if strings.Contains(string(source), value) {
				t.Errorf("Runtime production source %s reintroduced Scheduler-owned lifecycle object %s", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerApplicationDoesNotOwnRecordTimersOrRuntimeDomainPolicy(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "..", "domain", "scheduler")); !os.IsNotExist(err) {
		t.Fatalf("Runtime domain/scheduler must stay absent after policy moved to the Scheduler SDK: %v", err)
	}
	root := filepath.Join("..", "..", "application", "scheduler")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{"RecordTimer", "record_timer", "runtime/domain/scheduler", "report_export", "ScheduledDispatchRecordMutation", "RecordInternalMutation"} {
			if strings.Contains(string(source), forbidden) {
				t.Errorf("Scheduler application source %s contains foreign owner coupling %q", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
