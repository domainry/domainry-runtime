package runtime

import (
	"os"
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
