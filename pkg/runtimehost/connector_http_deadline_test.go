package runtimehost

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
)

type deadlineRoundTripper func(*http.Request) (*http.Response, error)

func (f deadlineRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestConnectorHTTPUsesOperationDeadlineInsteadOfFixedClientTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	client := &http.Client{
		Timeout: time.Millisecond,
		Transport: deadlineRoundTripper(func(request *http.Request) (*http.Response, error) {
			gotDeadline, ok := request.Context().Deadline()
			if !ok || !gotDeadline.Equal(wantDeadline) {
				t.Errorf("transport deadline=%v want operation deadline=%v", gotDeadline, wantDeadline)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("receipt"))}, nil
		}),
	}
	transport := &connectorTransport{httpClient: client}
	response, err := transport.RoundTripHTTP(ctx, connector.HTTPRequest{Method: http.MethodPost, URL: "https://proxy.example.test/llm/expense/parse"})
	if err != nil || string(response.Body) != "receipt" {
		t.Fatalf("response=%+v error=%v", response, err)
	}
	if client.Timeout != time.Millisecond {
		t.Fatal("shared HTTP client timeout was mutated")
	}
}

func TestConnectorHTTPKeepsDefaultBoundWithoutOperationDeadline(t *testing.T) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: deadlineRoundTripper(func(request *http.Request) (*http.Response, error) {
			deadline, ok := request.Context().Deadline()
			if remaining := time.Until(deadline); !ok || remaining <= 0 || remaining > 30*time.Second {
				t.Errorf("unbounded request lost the default client timeout: deadline=%v", deadline)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
		}),
	}
	_, err := (&connectorTransport{httpClient: client}).RoundTripHTTP(context.Background(), connector.HTTPRequest{Method: http.MethodGet, URL: "https://proxy.example.test"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConnectorHTTPStillCancelsExpiredOperation(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: deadlineRoundTripper(func(request *http.Request) (*http.Response, error) {
			return nil, request.Context().Err()
		}),
	}
	_, err := (&connectorTransport{httpClient: client}).RoundTripHTTP(ctx, connector.HTTPRequest{Method: http.MethodPost, URL: "https://proxy.example.test/llm/expense/parse"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired operation was not cancelled: %v", err)
	}
}
