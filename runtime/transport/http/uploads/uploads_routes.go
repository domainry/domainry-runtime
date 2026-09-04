package uploads

import "net/http"

func (h *UploadsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /uploads", h.uploadFile)
	mux.HandleFunc("GET /uploads/{fileID}/scan", h.fileScanStatus)
	mux.HandleFunc("GET /uploads/{filename}", h.serveUploadedFile)
}
