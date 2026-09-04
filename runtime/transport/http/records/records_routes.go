package records

import "net/http"

func (h *RecordsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /records/objects/{objectKey}/records", h.listRecords)
	mux.HandleFunc("GET /records/objects/{objectKey}/fields/{fieldKey}/reference-options", h.referenceOptions)
	mux.HandleFunc("POST /records/objects/{objectKey}/records/export", h.dispatchExport)
	mux.HandleFunc("GET /records/exports/{jobID}/download", h.downloadExport)
	mux.HandleFunc("POST /records/objects/{objectKey}/records/import/preview", h.previewImport)
	mux.HandleFunc("POST /records/objects/{objectKey}/records/import/apply", h.applyImport)
	mux.HandleFunc("POST /records/objects/{objectKey}/records/import/jobs", h.enqueueImportJob)
	mux.HandleFunc("POST /records/objects/{objectKey}/records", h.createRecord)
	mux.HandleFunc("GET /records/objects/{objectKey}/records/{recordID}", h.getRecord)
	mux.HandleFunc("GET /records/objects/{objectKey}/records/{recordID}/references", h.recordReferences)
	mux.HandleFunc("GET /records/objects/{objectKey}/records/{recordID}/related/{relatedObjectKey}", h.relatedRecords)
	mux.HandleFunc("PATCH /records/objects/{objectKey}/records/{recordID}", h.updateRecord)
	mux.HandleFunc("POST /records/objects/{objectKey}/records/{recordID}/deactivate-profile", h.deactivateBusinessProfile)
	mux.HandleFunc("POST /records/objects/{objectKey}/records/{recordID}/reactivate-profile", h.reactivateBusinessProfile)
	mux.HandleFunc("DELETE /records/objects/{objectKey}/records/{recordID}", h.deleteRecord)
	mux.HandleFunc("GET /records/objects/{objectKey}/actions", h.listActions)
	mux.HandleFunc("POST /records/objects/{objectKey}/actions/{actionKey}/run", h.executeObjectAction)
	mux.HandleFunc("POST /records/objects/{objectKey}/actions/{actionKey}/bulk", h.executeBulkAction)
	mux.HandleFunc("POST /records/objects/{objectKey}/records/{recordID}/actions/{actionKey}", h.executeAction)
	mux.HandleFunc("GET /records/permissions/effective", h.effectivePermissions)
	mux.HandleFunc("GET /records/stream", h.streamBusinessRecords)
}
