// Package runtimefile defines the deployment-owned binary storage and file
// scanning ports consumed by Runtime. These are infrastructure adapters, not
// project business extensions, and never receive repositories or database
// handles.
package runtimefile

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrBlobNotFound         = errors.New("runtime blob not found")
	ErrBlobTooLarge         = errors.New("runtime blob exceeds maximum size")
	ErrBlobIdentityConflict = errors.New("runtime blob identity conflict")
)

type AdapterDescriptor struct {
	Provider string `json:"provider"`
	Revision string `json:"revision"`
}

type BlobStageRequest struct {
	WorkspaceID string
	StageID     string
	Content     io.Reader
	MaxBytes    int64
}

type BlobCommitRequest struct {
	WorkspaceID   string
	StageKey      string
	BlobKey       string
	ContentSHA256 string
	Size          int64
}

type BlobInfo struct {
	WorkspaceID   string    `json:"workspace_id"`
	BlobKey       string    `json:"blob_key"`
	ContentSHA256 string    `json:"content_sha256"`
	Size          int64     `json:"size"`
	ModifiedAt    time.Time `json:"modified_at,omitempty"`
}

// BlobStore owns byte persistence. Stage must isolate incomplete content;
// Commit must atomically make the immutable BlobKey visible or return an
// identity conflict. Repeating Commit for identical content is successful,
// and Delete is idempotent when the blob is already absent.
type BlobStore interface {
	Descriptor() AdapterDescriptor
	Stage(context.Context, BlobStageRequest) (BlobInfo, error)
	Commit(context.Context, BlobCommitRequest) (BlobInfo, error)
	Open(context.Context, string, string) (io.ReadCloser, error)
	Stat(context.Context, string, string) (BlobInfo, error)
	Delete(context.Context, string, string) error
}

type FileScanRequest struct {
	WorkspaceID   string
	FileID        string
	BlobKey       string
	Filename      string
	ContentType   string
	ContentSHA256 string
	Size          int64
}

type FileScanResult struct {
	Status      string
	Provider    string
	EvidenceRef string
}

// FileScanner consumes the exact immutable blob stream selected by Runtime.
// Implementations must read to EOF before returning a terminal result. A
// transient adapter error is returned as error so the durable scan stays
// pending and can be retried.
type FileScanner interface {
	Descriptor() AdapterDescriptor
	Scan(context.Context, FileScanRequest, io.Reader) (FileScanResult, error)
}
