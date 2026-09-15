package codingruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (r *Runtime) fileRead(raw json.RawMessage) agentsdk.ConversationToolResult {
	var in struct {
		Path     string `json:"path"`
		Offset   int    `json:"offset"`
		MaxBytes int    `json:"max_bytes"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return failed("coding_input_invalid")
	}
	if in.MaxBytes == 0 {
		in.MaxBytes = 32768
	}
	path, err := cleanRelative(in.Path)
	if err != nil {
		return failed("coding_path_invalid")
	}
	info, err := r.root.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return failed("coding_file_unavailable")
	}
	data, err := r.root.ReadFile(path)
	if err != nil || !utf8.Valid(data) {
		return failed("coding_file_unavailable")
	}
	if in.Offset > len(data) {
		return failed("coding_offset_invalid")
	}
	end := min(len(data), in.Offset+in.MaxBytes)
	return completed(map[string]any{"kind": "file_read", "path": path, "content": string(data[in.Offset:end]), "offset": in.Offset, "next_offset": end, "complete": end == len(data), "size": len(data), "sha256": digest(data)})
}

func (r *Runtime) fileSearch(ctx context.Context, raw json.RawMessage) agentsdk.ConversationToolResult {
	var in struct {
		Query, Path, Glob string
		Limit             int
	}
	if json.Unmarshal(raw, &in) != nil || in.Query == "" {
		return failed("coding_input_invalid")
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	base := "."
	var err error
	if in.Path != "" && strings.TrimSpace(in.Path) != "." {
		base, err = cleanRelative(in.Path)
		if err != nil {
			return failed("coding_path_invalid")
		}
	}
	type match struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Text string `json:"text"`
	}
	items := []match{}
	truncated := false
	err = fs.WalkDir(r.root.FS(), base, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fs.SkipDir
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor" {
				return fs.SkipDir
			}
			return nil
		}
		if in.Glob != "" {
			ok, _ := filepath.Match(in.Glob, filepath.Base(path))
			if !ok {
				return nil
			}
		}
		info, e := d.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			return nil
		}
		data, e := r.root.ReadFile(path)
		if e != nil || !utf8.Valid(data) {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, in.Query) {
				text := line
				if len(text) > 512 {
					text = text[:512]
				}
				items = append(items, match{path, i + 1, text})
				if len(items) >= in.Limit {
					truncated = true
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return failed("coding_search_failed")
	}
	return completed(map[string]any{"kind": "file_search", "query": in.Query, "matches": items, "truncated": truncated})
}

func (r *Runtime) fileWrite(raw json.RawMessage, edit, reconcile bool) agentsdk.ConversationToolResult {
	r.fileMu.Lock()
	defer r.fileMu.Unlock()

	var in struct {
		Path              string `json:"path"`
		Content           string `json:"content"`
		ExpectedSHA256    string `json:"expected_sha256"`
		OldText           string `json:"old_text"`
		NewText           string `json:"new_text"`
		CreateDirectories bool   `json:"create_directories"`
		ReplaceAll        bool   `json:"replace_all"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return failed("coding_input_invalid")
	}
	path, err := cleanRelative(in.Path)
	if err != nil {
		return failed("coding_path_invalid")
	}
	before, readErr := r.root.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return failed("coding_file_unavailable")
	}
	if exists && !utf8.Valid(before) {
		return failed("coding_file_unavailable")
	}
	desired := []byte(in.Content)
	if reconcile && !edit && exists && digest(before) == digest(desired) {
		return completed(map[string]any{"kind": "file_change", "operation": "write", "path": path, "previous_sha256": in.ExpectedSHA256, "sha256": digest(before), "bytes": len(before), "reconciled": true})
	}
	if in.ExpectedSHA256 == "missing" {
		if exists {
			if reconcile {
				return agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "coding_receipt_unavailable", Content: json.RawMessage(`{"error":"coding_receipt_unavailable"}`)}
			}
			return failed("coding_version_conflict")
		}
	} else if !exists || digest(before) != in.ExpectedSHA256 {
		if reconcile {
			return agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "coding_receipt_unavailable", Content: json.RawMessage(`{"error":"coding_receipt_unavailable"}`)}
		}
		return failed("coding_version_conflict")
	}
	after := desired
	if edit {
		count := strings.Count(string(before), in.OldText)
		if count == 0 || count > 1 && !in.ReplaceAll {
			return failed("coding_edit_match_invalid")
		}
		n := 1
		if in.ReplaceAll {
			n = -1
		}
		after = []byte(strings.Replace(string(before), in.OldText, in.NewText, n))
	}
	if !utf8.Valid(after) || len(after) > 1<<20 {
		return failed("coding_file_invalid")
	}
	if in.CreateDirectories {
		if err = r.root.MkdirAll(filepath.ToSlash(filepath.Dir(path)), 0755); err != nil {
			return failed("coding_write_failed")
		}
	}
	mode := fs.FileMode(0644)
	if exists {
		if info, statErr := r.root.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
	}
	tmp, file, err := r.openTemporaryFile(path, mode)
	if err != nil {
		return failed("coding_write_failed")
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = r.root.Remove(tmp)
		}
	}()
	written, writeErr := file.Write(after)
	if writeErr != nil || written != len(after) || file.Sync() != nil || file.Close() != nil {
		return failed("coding_write_failed")
	}
	if err = r.root.Rename(tmp, path); err != nil {
		return failed("coding_write_failed")
	}
	removeTemporary = false
	return completed(map[string]any{"kind": "file_change", "operation": map[bool]string{false: "write", true: "edit"}[edit], "path": path, "previous_sha256": map[bool]string{false: "missing", true: digest(before)}[exists], "sha256": digest(after), "bytes": len(after)})
}

func (r *Runtime) openTemporaryFile(path string, mode fs.FileMode) (string, *os.File, error) {
	directory, base := filepath.ToSlash(filepath.Dir(path)), filepath.Base(path)
	for range 8 {
		random := make([]byte, 12)
		if _, err := rand.Read(random); err != nil {
			return "", nil, err
		}
		name := filepath.ToSlash(filepath.Join(directory, "."+base+".domainry-"+hex.EncodeToString(random)+".tmp"))
		file, err := r.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return name, file, err
	}
	return "", nil, errors.New("coding temporary file name unavailable")
}
