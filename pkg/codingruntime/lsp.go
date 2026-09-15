package codingruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type lspMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}
type lspLocation struct {
	URI                  string    `json:"uri"`
	Range                lspRange  `json:"range"`
	TargetURI            string    `json:"targetUri"`
	TargetSelectionRange *lspRange `json:"targetSelectionRange"`
}

func writeLSP(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(raw))
	if err == nil {
		_, err = w.Write(raw)
	}
	return err
}
func readLSP(r *bufio.Reader) (lspMessage, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return lspMessage{}, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			length, err = strconv.Atoi(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]))
			if err != nil {
				return lspMessage{}, err
			}
		}
	}
	if length < 0 || length > 4<<20 {
		return lspMessage{}, errors.New("invalid lsp frame")
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(r, raw); err != nil {
		return lspMessage{}, err
	}
	var msg lspMessage
	if json.Unmarshal(raw, &msg) != nil {
		return lspMessage{}, errors.New("invalid lsp message")
	}
	return msg, nil
}

func awaitLSP(r *bufio.Reader, w io.Writer, id int) (json.RawMessage, error) {
	want := strconv.Itoa(id)
	for i := 0; i < 64; i++ {
		msg, err := readLSP(r)
		if err != nil {
			return nil, err
		}
		if len(msg.ID) > 0 && string(msg.ID) == want {
			if msg.Error != nil {
				return nil, errors.New("lsp operation failed")
			}
			return msg.Result, nil
		}
		if msg.Method != "" && len(msg.ID) > 0 {
			_ = writeLSP(w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "error": map[string]any{"code": -32601, "message": "unsupported server request"}})
		}
	}
	return nil, errors.New("lsp response limit")
}

func (r *Runtime) lspQuery(ctx context.Context, raw json.RawMessage) agentsdk.ConversationToolResult {
	var in struct {
		Operation, Path string
		Line, Character int
	}
	if json.Unmarshal(raw, &in) != nil || in.Line < 1 || in.Character < 1 {
		return failed("coding_input_invalid")
	}
	path, err := cleanRelative(in.Path)
	if err != nil {
		return failed("coding_path_invalid")
	}
	server, ok := r.lsp[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return failed("coding_lsp_unavailable")
	}
	data, err := r.root.ReadFile(path)
	if err != nil || len(data) > 2<<20 || !utf8.Valid(data) {
		return failed("coding_file_unavailable")
	}
	cmd, err := r.sandboxCommand(ctx, "", server.Command)
	if err != nil {
		return failed("coding_lsp_unavailable")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return failed("coding_lsp_unavailable")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failed("coding_lsp_unavailable")
	}
	if err = cmd.Start(); err != nil {
		return failed("coding_lsp_unavailable")
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	reader := bufio.NewReader(stdout)
	rootURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(r.rootPath)}).String()
	fileURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(r.rootPath, filepath.FromSlash(path)))}).String()
	if writeLSP(stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"processId": nil, "rootUri": rootURI, "capabilities": map[string]any{"general": map[string]any{"positionEncodings": []string{"utf-16"}}}, "workspaceFolders": []any{map[string]any{"uri": rootURI, "name": filepath.Base(r.rootPath)}}}}) != nil {
		return failed("coding_lsp_failed")
	}
	initRaw, err := awaitLSP(reader, stdin, 1)
	if err != nil {
		return failed("coding_lsp_failed")
	}
	var init struct {
		Capabilities struct {
			PositionEncoding   string `json:"positionEncoding"`
			DefinitionProvider any    `json:"definitionProvider"`
			ReferencesProvider any    `json:"referencesProvider"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(initRaw, &init) != nil || init.Capabilities.PositionEncoding != "" && init.Capabilities.PositionEncoding != "utf-16" {
		return failed("coding_lsp_malformed")
	}
	if in.Operation == "definition" && !lspCapabilityEnabled(init.Capabilities.DefinitionProvider) || in.Operation == "references" && !lspCapabilityEnabled(init.Capabilities.ReferencesProvider) {
		return failed("coding_lsp_unsupported")
	}
	_ = writeLSP(stdin, map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}})
	_ = writeLSP(stdin, map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI, "languageId": server.LanguageID, "version": 1, "text": string(data)}}})
	method := "textDocument/definition"
	params := map[string]any{"textDocument": map[string]any{"uri": fileURI}, "position": map[string]any{"line": in.Line - 1, "character": in.Character - 1}}
	if in.Operation == "references" {
		method = "textDocument/references"
		params["context"] = map[string]any{"includeDeclaration": true}
	} else if in.Operation != "definition" {
		return failed("coding_input_invalid")
	}
	if writeLSP(stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": method, "params": params}) != nil {
		return failed("coding_lsp_failed")
	}
	resultRaw, err := awaitLSP(reader, stdin, 2)
	if err != nil {
		return failed("coding_lsp_failed")
	}
	locations, err := r.normalizeLocations(resultRaw)
	if err != nil {
		return failed("coding_lsp_malformed")
	}
	_ = writeLSP(stdin, map[string]any{"jsonrpc": "2.0", "method": "textDocument/didClose", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI}}})
	return completed(map[string]any{"kind": "lsp", "operation": in.Operation, "path": path, "line": in.Line, "character": in.Character, "locations": locations, "truncated": false})
}

func lspCapabilityEnabled(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case map[string]any:
		return typed != nil
	default:
		return false
	}
}

func (r *Runtime) normalizeLocations(raw json.RawMessage) ([]map[string]any, error) {
	if string(raw) == "null" || len(raw) == 0 {
		return []map[string]any{}, nil
	}
	var list []lspLocation
	if raw[0] == '[' {
		if json.Unmarshal(raw, &list) != nil {
			return nil, errors.New("invalid")
		}
	} else {
		var one lspLocation
		if json.Unmarshal(raw, &one) != nil {
			return nil, errors.New("invalid")
		}
		list = []lspLocation{one}
	}
	if len(list) > 200 {
		list = list[:200]
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		uriText := item.URI
		rng := item.Range
		if item.TargetURI != "" {
			uriText = item.TargetURI
			if item.TargetSelectionRange != nil {
				rng = *item.TargetSelectionRange
			}
		}
		parsed, err := url.Parse(uriText)
		if err != nil || parsed.Scheme != "file" {
			return nil, errors.New("invalid")
		}
		absolute, err := filepath.Abs(filepath.FromSlash(parsed.Path))
		if err != nil {
			return nil, errors.New("invalid")
		}
		relative, err := filepath.Rel(r.rootPath, absolute)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, errors.New("invalid")
		}
		out = append(out, map[string]any{"path": filepath.ToSlash(relative), "start": map[string]int{"line": rng.Start.Line + 1, "character": rng.Start.Character + 1}, "end": map[string]int{"line": rng.End.Line + 1, "character": rng.End.Character + 1}})
	}
	return out, nil
}
