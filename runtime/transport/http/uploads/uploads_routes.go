package uploads

import "net/http"

func (h *UploadsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /files", h.uploadFile)
	mux.HandleFunc("GET /files/{fileID}/scan", h.fileScanStatus)
	mux.HandleFunc("GET /uploads/{filename}", h.serveUploadedFile)
}
