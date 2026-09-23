// Package runtimegin contains the optional Gin transport adapter for
// project-owned HTTP APIs. It maps Runtime errors but never defines project
// routes, DTOs or business validation.
package runtimegin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	"github.com/gin-gonic/gin"
)

type ErrorBody struct {
	Error Error `json:"error"`
}

type Error struct {
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

// RequestValidationError is returned by a project HTTP DTO's Validate method
// when a rule belongs to that specific endpoint rather than Runtime metadata.
type RequestValidationError struct {
	Code    string
	Message string
}

func (e *RequestValidationError) Error() string { return e.Message }

func Invalid(code, message string) error {
	return &RequestValidationError{Code: strings.TrimSpace(code), Message: strings.TrimSpace(message)}
}

// BindJSON runs Gin JSON binding (including binding tags) and then an optional
// project-defined Validate method. Domain field validation is deliberately not
// duplicated here: Engine Create/Update and InvokeAction perform Runtime
// metadata validation after binding succeeds.
func BindJSON(context *gin.Context, target any) bool {
	if err := context.ShouldBindJSON(target); err != nil {
		WriteValidationError(context, "body", "request body is invalid")
		return false
	}
	validator, ok := target.(interface{ Validate() error })
	if !ok {
		return true
	}
	if err := validator.Validate(); err != nil {
		var validation *RequestValidationError
		if errors.As(err, &validation) {
			WriteValidationError(context, validation.Code, validation.Message)
		} else {
			WriteValidationError(context, "request", "request body failed project validation")
		}
		return false
	}
	return true
}

func WriteError(context *gin.Context, err error) {
	status := runtimeengine.HTTPStatus(err)
	code := runtimeengine.ErrorCode(err)
	message := code
	parameters := map[string]string(nil)
	if typed, ok := runtimeengine.AsError(err); ok {
		parameters = typed.Parameters
		if status < http.StatusInternalServerError && strings.TrimSpace(typed.Message) != "" {
			message = typed.Message
		}
	}
	context.AbortWithStatusJSON(status, ErrorBody{Error: Error{Code: code, Message: message, Parameters: parameters}})
}

func WriteValidationError(context *gin.Context, code, message string) {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "request"
	}
	context.AbortWithStatusJSON(http.StatusBadRequest, ErrorBody{Error: Error{Code: "invalid_" + code, Message: strings.TrimSpace(message)}})
}

func RequireIdempotencyKey(context *gin.Context) (string, bool) {
	key := strings.TrimSpace(context.GetHeader("Idempotency-Key"))
	if key == "" {
		WriteValidationError(context, "idempotency_key", "Idempotency-Key header is required")
		return "", false
	}
	return key, true
}
