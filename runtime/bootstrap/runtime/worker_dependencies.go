package runtime

import (
	"fmt"

	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func newRuntimeWorkerDependencies(instanceID string) (workerplatform.Dependencies, error) {
	workerID, err := workerplatform.NewWorkerID(instanceID)
	if err != nil {
		return workerplatform.Dependencies{}, fmt.Errorf("create Runtime worker identity: %w", err)
	}
	return workerplatform.NewDependencies(workerID), nil
}
