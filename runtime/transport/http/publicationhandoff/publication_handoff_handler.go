package publicationhandoff

import (
	"context"
	"net/http"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
)

type IntentReader interface {
	GetBusinessPublicationHandoff(context.Context, string, principalmodel.Principal) (publicationmodel.Handoff, error)
}

type Handler struct {
	intents           IntentReader
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	authenticated     func(http.HandlerFunc) http.HandlerFunc
}

type Dependencies struct {
	Intents           IntentReader
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
}

func NewHandler(deps Dependencies) *Handler {
	return &Handler{intents: deps.Intents, principal: deps.Principal, writeJSON: deps.WriteJSON, writeServiceError: deps.WriteServiceError, authenticated: deps.Authenticated}
}
