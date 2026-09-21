package uploads

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
)

func FuzzLocalBlobStoreWorkspaceIdentityCannotCrossScope(f *testing.F) {
	for _, seed := range []string{"workspace-a", "../workspace-b", "/tmp/escape", "..\\escape", "workspace/child", "\x00"} {
		f.Add(seed)
	}
	store, err := blobstore.NewLocalStore(f.TempDir())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, workspaceID string) {
		staged, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{WorkspaceID: workspaceID, StageID: "fuzz", Content: bytes.NewReader([]byte("x")), MaxBytes: 1})
		if strings.TrimSpace(workspaceID) == "" {
			if err == nil {
				t.Fatal("empty workspace accepted")
			}
			return
		}
		if err != nil {
			return
		}
		key := staged.ContentSHA256 + "-fuzz.bin"
		if _, err := store.Commit(t.Context(), runtimefile.BlobCommitRequest{WorkspaceID: workspaceID, StageKey: staged.BlobKey, BlobKey: key, ContentSHA256: staged.ContentSHA256, Size: staged.Size}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Stat(t.Context(), workspaceID+"-other", key); !errors.Is(err, runtimefile.ErrBlobNotFound) {
			t.Fatalf("workspace=%q crossed scope: %v", workspaceID, err)
		}
	})
}
