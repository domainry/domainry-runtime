package composition

import publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"

func publicationHandoffApplication(records *runtimeAssembly) *publicationhandoff.PublicationHandoffApplicationService {
	if records == nil {
		return publicationhandoff.NewPublicationHandoffApplicationService(publicationhandoff.Dependencies{})
	}
	return publicationhandoff.NewPublicationHandoffApplicationService(publicationhandoff.Dependencies{
		Repository:       records.integrationPublicationRepo,
		WorkerRepository: records.integrationPublicationWorkerRepo,
		Delivery:         records.integrationOwnerDelivery,
		Worker:           records.workerDependencies,
		Wakeups:          records.workerWakeups,
		PreparePayload:   records.prepareOutboxPayload,
	})
}
