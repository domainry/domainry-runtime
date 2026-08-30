package composition

import publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"

func publicationHandoffApplication(records *runtimeAssembly) *publicationhandoff.Service {
	if records == nil {
		return publicationhandoff.New(publicationhandoff.Dependencies{})
	}
	return publicationhandoff.New(publicationhandoff.Dependencies{
		Repository:       records.integrationPublicationRepo,
		WorkerRepository: records.integrationPublicationWorkerRepo,
		Delivery:         records.integrationOwnerDelivery,
		Worker:           records.workerDependencies,
		Wakeups:          records.workerWakeups,
		PreparePayload:   records.prepareOutboxPayload,
	})
}
