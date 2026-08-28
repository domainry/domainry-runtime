package integration

import (
	"context"
	"errors"
	"testing"
	"time"
)

type referenceCancelContext struct{ calls, cancelAt int }

func (*referenceCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*referenceCancelContext) Done() <-chan struct{}       { return nil }
func (c *referenceCancelContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
func (*referenceCancelContext) Value(any) any { return nil }

func TestFindConnectionReferencesAggregatesAndSortsResources(t *testing.T) {
	resources := []ConnectionReferenceResource{
		{Kind: "workflow", Key: "z", Value: map[string]any{"steps": []any{map[string]any{"connection_key": "primary"}}}},
		{Kind: "action", Key: "a", Value: map[string]any{"connection_key": "primary"}},
		{Kind: "action", Key: "a", Value: map[string]any{"nested": map[string]any{"connection_key": "other"}}},
	}
	references, err := FindConnectionReferences(t.Context(), resources, "primary")
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 2 || references[0].Kind != "action" || references[0].Path != "connection_key" || references[1].Kind != "workflow" || references[1].Path != "steps[0].connection_key" {
		t.Fatalf("references=%#v", references)
	}
}

func TestFindConnectionReferencePathEdges(t *testing.T) {
	if paths, err := FindConnectionReferencePaths(t.Context(), map[string]any{"invalid": func() {}}, "connection"); err != nil || len(paths) != 1 || paths[0] != "encoding_error" {
		t.Fatalf("encoding paths=%#v err=%v", paths, err)
	}
	if paths, err := FindConnectionReferencePaths(t.Context(), map[string]any{"items": []any{map[string]any{"connection_key": "connection"}}, "nested": map[string]any{"connection_key": "connection"}}, "connection"); err != nil || len(paths) != 2 {
		t.Fatalf("nested paths=%#v err=%v", paths, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := FindConnectionReferencePaths(cancelled, map[string]any{"connection_key": "connection"}, "connection"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled path error=%v", err)
	}
	if _, err := FindConnectionReferences(cancelled, []ConnectionReferenceResource{{Value: map[string]any{"connection_key": "connection"}}}, "connection"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled references error=%v", err)
	}
	if _, err := findConnectionReferencePaths(&referenceCancelContext{cancelAt: 2}, map[string]any{"child": map[string]any{}}, "connection", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("map child cancellation error=%v", err)
	}
	if _, err := findConnectionReferencePaths(&referenceCancelContext{cancelAt: 2}, []any{map[string]any{}}, "connection", "items"); !errors.Is(err, context.Canceled) {
		t.Fatalf("slice child cancellation error=%v", err)
	}
	resources := []ConnectionReferenceResource{
		{Kind: "same", Key: "z", Value: map[string]any{"connection_key": "connection"}},
		{Kind: "same", Key: "a", Value: map[string]any{"z": map[string]any{"connection_key": "connection"}, "a": map[string]any{"connection_key": "connection"}}},
	}
	if references, err := FindConnectionReferences(t.Context(), resources, "connection"); err != nil || len(references) != 3 || references[0].Key != "a" || references[0].Path >= references[1].Path {
		t.Fatalf("same-kind references=%#v err=%v", references, err)
	}
}
