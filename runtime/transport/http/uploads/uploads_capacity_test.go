package uploads

import (
	"bytes"
	"errors"
	"io"
	"syscall"
	"testing"
)

func TestCopyBoundedUploadStreamsWithinLimitAndRejectsOverflow(t *testing.T) {
	var output bytes.Buffer
	size, err := copyBoundedUpload(&output, []byte("head"), bytes.NewReader([]byte("body")), io.Copy)
	if err != nil || size != 8 || output.String() != "headbody" {
		t.Fatalf("size=%d output=%q err=%v", size, output.String(), err)
	}
	output.Reset()
	if _, err := copyBoundedUpload(&output, nil, bytes.NewReader(bytes.Repeat([]byte{'x'}, maxUploadBytes+1)), io.Copy); !errors.Is(err, errUploadTooLarge) {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestCopyBoundedUploadPreservesStorageExhaustion(t *testing.T) {
	_, err := copyBoundedUpload(io.Discard, []byte("head"), bytes.NewReader([]byte("body")), func(io.Writer, io.Reader) (int64, error) { return 0, syscall.ENOSPC })
	if !uploadStorageExhausted(err) {
		t.Fatalf("storage exhaustion lost: %v", err)
	}
}
