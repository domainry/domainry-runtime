package records

import "net/http"

func (h *RecordsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /records/{objectKey}", h.listRecords)
	mux.HandleFunc("GET /records/{objectKey}/fields/{fieldKey}/options", h.referenceOptions)
	mux.HandleFunc("POST /records/{objectKey}/export", h.dispatchExport)
	mux.HandleFunc("GET /records/exports/jobs/{jobID}", h.downloadExport)
	mux.HandleFunc("POST /records/{objectKey}/import/preview", h.previewImport)
	mux.HandleFunc("POST /records/{objectKey}/import/apply", h.applyImport)
	mux.HandleFunc("POST /records/{objectKey}/import/jobs", h.enqueueImportJob)
	mux.HandleFunc("POST /records/{objectKey}", h.createRecord)
	mux.HandleFunc("GET /records/{objectKey}/items/{recordID}", h.getRecord)
	mux.HandleFunc("GET /records/{objectKey}/items/{recordID}/references", h.recordReferences)
	mux.HandleFunc("GET /records/{objectKey}/items/{recordID}/related/{relatedObjectKey}", h.relatedRecords)
	mux.HandleFunc("PATCH /records/{objectKey}/items/{recordID}", h.updateRecord)
	mux.HandleFunc("POST /records/{objectKey}/items/{recordID}/profile/deactivate", h.deactivateBusinessProfile)
	mux.HandleFunc("POST /records/{objectKey}/items/{recordID}/profile/reactivate", h.reactivateBusinessProfile)
	mux.HandleFunc("DELETE /records/{objectKey}/items/{recordID}", h.deleteRecord)
	mux.HandleFunc("GET /records/{objectKey}/actions", h.listActions)
	mux.HandleFunc("POST /records/{objectKey}/actions/{actionKey}", h.executeObjectAction)
	mux.HandleFunc("POST /records/{objectKey}/actions/{actionKey}/bulk", h.executeBulkAction)
	mux.HandleFunc("POST /records/{objectKey}/items/{recordID}/actions/{actionKey}", h.executeAction)
	mux.HandleFunc("POST /records/action-assurance/challenges", h.beginActionAssurance)
	mux.HandleFunc("POST /records/action-assurance/challenges/verify", h.verifyActionAssurance)
	mux.HandleFunc("GET /records/permissions/effective", h.effectivePermissions)
	mux.HandleFunc("GET /records/stream", h.streamBusinessRecords)
}
