package party

import "net/http"

func (h *PartyHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /party", h.dependencies.Authenticated(h.list))
	mux.HandleFunc("GET /party/{partyID}", h.dependencies.Authenticated(h.get))
	mux.HandleFunc("PUT /party/{partyID}", h.dependencies.Authenticated(h.upsert))
	mux.HandleFunc("GET /foundation/jobs", h.dependencies.Authenticated(h.listJobs))
	mux.HandleFunc("GET /foundation/jobs/{jobID}", h.dependencies.Authenticated(h.getJob))
	mux.HandleFunc("PUT /foundation/jobs/{jobID}", h.dependencies.Authenticated(h.upsertJob))
	mux.HandleFunc("GET /foundation/positions", h.dependencies.Authenticated(h.listPositions))
	mux.HandleFunc("GET /foundation/positions/{positionID}", h.dependencies.Authenticated(h.getPosition))
	mux.HandleFunc("PUT /foundation/positions/{positionID}", h.dependencies.Authenticated(h.upsertPosition))
	mux.HandleFunc("GET /foundation/organization-extensions", h.dependencies.Authenticated(h.listOrganizationExtensions))
	mux.HandleFunc("PUT /foundation/organization-extensions/{extensionID}", h.dependencies.Authenticated(h.upsertOrganizationExtension))
	mux.HandleFunc("GET /foundation/organization-memberships", h.dependencies.Authenticated(h.listOrganizationMemberships))
	mux.HandleFunc("PUT /foundation/organization-memberships/{membershipID}", h.dependencies.Authenticated(h.upsertOrganizationMembership))
}
