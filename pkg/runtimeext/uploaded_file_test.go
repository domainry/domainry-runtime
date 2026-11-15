package runtimeext

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type nativeUploadInput struct {
	ContentSHA256 string    `json:"content_sha256"`
	Size          int64     `json:"size"`
	Content       io.Reader `json:"-"`
}

func TestUploadNativeStreamNeverEntersSerializedPayload(t *testing.T) {
	input := nativeUploadInput{ContentSHA256: strings.Repeat("a", 64), Size: 7, Content: strings.NewReader("private")}
	fields := TypedPayloadFields[nativeUploadInput]()
	encoded, err := json.Marshal(input)
	if err != nil || len(fields) != 2 || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "Content") {
		t.Fatalf("stream leaked into action metadata %s %v", encoded, err)
	}
	descriptor := typedHandlerDescriptor()
	descriptor.FileCapabilities = []string{FileOperationIngestUpload, FileOperationInspectUpload}
	handler, err := NewTypedBusinessHandler(descriptor, func(ActionExecution) (bool, error) { return true, nil }, func(_ context.Context, _ bool, input nativeUploadInput) (map[string]any, error) {
		if input.Content == nil {
			return nil, errors.New("stream required")
		}
		content, err := io.ReadAll(input.Content)
		return map[string]any{"size": len(content)}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	result, accepted, err := handler.(NativeBusinessHandler).InvokeNative(t.Context(), typedHandlerExecution{actionKey: descriptor.ActionKey}, input)
	if err != nil || !accepted || string(result) != `{"size":7}` {
		t.Fatalf("native stream unavailable: %s %v", result, err)
	}
	if _, err := handler.Invoke(t.Context(), typedHandlerExecution{actionKey: descriptor.ActionKey}, encoded); err == nil {
		t.Fatal("generic JSON created a native upload stream")
	}
	if _, err := IngestUploadedFile(t.Context(), typedHandlerExecution{}, UploadedFileRequest{}); err == nil {
		t.Fatal("missing optional upload port did not fail closed")
	}
	if _, err := InspectUploadedFile(t.Context(), typedHandlerExecution{}, UploadedFileInspection{}); err == nil {
		t.Fatal("missing optional inspection port did not fail closed")
	}
}
