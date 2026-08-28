package uploads

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzWorkspaceUploadDirCannotEscapeRoot(f *testing.F) {
	for _, seed := range []string{"workspace-a", "../workspace-b", "/tmp/escape", "..\\escape", "workspace/child", "\x00"} {
		f.Add(seed)
	}
	root := f.TempDir()
	handler := &UploadsHandler{uploadDir: root}
	f.Fuzz(func(t *testing.T, workspaceID string) {
		target, err := handler.workspaceUploadDir(workspaceID)
		if strings.TrimSpace(workspaceID) == "" {
			if err == nil {
				t.Fatal("empty workspace accepted")
			}
			return
		}
		if err != nil {
			return
		}
		relative, err := filepath.Rel(root, target)
		if err != nil || filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("workspace path escaped root: workspace=%q target=%q relative=%q err=%v", workspaceID, target, relative, err)
		}
	})
}
