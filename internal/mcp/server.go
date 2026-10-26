// Package mcp is a stdio MCP server with exactly two tools: capabilities and execute.
// execute dispatches the CLI in-process. It is not a shell.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/app-plat/app-plat/internal/cli"
)

const protocolVersion = "2024-11-05"

// Options configures a server process. APIURL, when set, overrides the saved
// API URL for execute calls that do not pass api-url themselves.
type Options struct {
	APIURL string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Serve reads newline-delimited JSON-RPC from r and writes responses to w.
// Logs go to errW only. A clean stdin EOF returns nil.
func Serve(r io.Reader, w, errW io.Writer, opts Options) error {
	if errW == nil {
		errW = io.Discard
	}
	dec := json.NewDecoder(r)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for {
		var msg json.RawMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			_ = writeError(enc, nil, -32700, "parse error")
			return nil
		}
		if err := handleMessage(msg, enc, errW, opts); err != nil {
			return err
		}
	}
}

func handleMessage(msg json.RawMessage, enc *json.Encoder, errW io.Writer, opts Options) error {
	var req rpcRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return writeError(enc, nil, -32700, "parse error")
	}
	if !hasID(req.ID) {
		return nil
	}
	if req.Method == "" {
		return writeError(enc, req.ID, -32600, "invalid request")
	}
	fmt.Fprintf(errW, "mcp: %s\n", req.Method)
	switch req.Method {
	case "initialize":
		return writeResult(enc, req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "app-plat", "version": "0.1.0"},
		})
	case "ping":
		return writeResult(enc, req.ID, map[string]any{})
	case "tools/list":
		return writeResult(enc, req.ID, map[string]any{"tools": toolDefs()})
	case "tools/call":
		result := callTool(req.Params, opts)
		return writeResult(enc, req.ID, result)
	default:
		return writeError(enc, req.ID, -32601, "method not found")
	}
}

func toolDefs() []toolDef {
	return []toolDef{
		{
			Name:        "capabilities",
			Description: "Help menu of every app-plat CLI command (name, args, one-line description). Optional query filters by substring. Auth comes from ~/.app-plat/config.json or APP_PLAT_API_URL / APP_PLAT_API_KEY.",
			InputSchema: objectSchema(map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Optional substring filter",
				},
			}, nil),
		},
		{
			Name:        "execute",
			Description: "Run an app-plat CLI command in-process using the saved API key and return its stdout. Not a shell. command is like \"apps list\"; args are positionals; flags maps flag names to values. Boolean flags: secret, text. Rejects mcp and shell metacharacters.",
			InputSchema: objectSchema(map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "CLI command such as \"apps list\"",
				},
				"args": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Positional arguments",
				},
				"flags": map[string]any{
					"type":        "object",
					"description": "Flag names to string, number, or boolean values",
				},
			}, []string{"command"}),
		},
	}
}

func objectSchema(props map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func callTool(params json.RawMessage, opts Options) toolResult {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(bytes.TrimSpace(params)) > 0 && !bytes.Equal(bytes.TrimSpace(params), []byte("null")) {
		if err := json.Unmarshal(params, &call); err != nil {
			return toolErr(fmt.Errorf("invalid tools/call params"))
		}
	}
	rawArgs := call.Arguments
	if len(bytes.TrimSpace(rawArgs)) == 0 || bytes.Equal(bytes.TrimSpace(rawArgs), []byte("null")) {
		rawArgs = []byte("{}")
	}
	switch call.Name {
	case "capabilities":
		var in struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(rawArgs, &in); err != nil {
			return toolErr(fmt.Errorf("invalid capabilities arguments"))
		}
		return toolOK(cli.CatalogText(in.Query))
	case "execute":
		text, err := executeTool(rawArgs, opts)
		if err != nil {
			return toolErr(err)
		}
		return toolOK(text)
	case "":
		return toolErr(fmt.Errorf("tool name is required"))
	default:
		return toolErr(fmt.Errorf("unknown tool %q", call.Name))
	}
}

func executeTool(raw json.RawMessage, opts Options) (string, error) {
	var in struct {
		Command string         `json:"command"`
		Args    []string       `json:"args"`
		Flags   map[string]any `json:"flags"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("invalid execute arguments")
	}
	argv, err := buildArgv(in.Command, in.Args, in.Flags)
	if err != nil {
		return "", err
	}
	if opts.APIURL != "" && !hasFlag(argv, "api-url") {
		argv = append(argv, "--api-url", opts.APIURL)
	}
	out, err := cli.Execute(context.Background(), argv)
	if err != nil {
		return "", err
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, nil
}

func buildArgv(command string, args []string, flags map[string]any) ([]string, error) {
	if err := rejectMeta("command", command); err != nil {
		return nil, err
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, fmt.Errorf("command is required")
	}
	if strings.EqualFold(fields[0], "mcp") {
		return nil, fmt.Errorf("command mcp is not allowed")
	}
	for _, arg := range args {
		if err := rejectMeta("arg", arg); err != nil {
			return nil, err
		}
	}
	argv := append([]string{}, fields...)
	argv = append(argv, args...)
	keys := make([]string, 0, len(flags))
	for k := range flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		name := strings.TrimLeft(strings.TrimSpace(key), "-")
		if name == "" {
			return nil, fmt.Errorf("invalid flag name")
		}
		if strings.EqualFold(name, "mcp") {
			return nil, fmt.Errorf("command mcp is not allowed")
		}
		if err := rejectMeta("flag", name); err != nil {
			return nil, err
		}
		val, err := flagString(flags[key])
		if err != nil {
			return nil, err
		}
		if err := rejectMeta("flag", val); err != nil {
			return nil, err
		}
		if name == "secret" || name == "text" {
			if isTruthy(val) {
				argv = append(argv, "--"+name)
			}
			continue
		}
		argv = append(argv, "--"+name, val)
	}
	return argv, nil
}

func hasFlag(argv []string, name string) bool {
	prefix := "--" + name
	for _, a := range argv {
		if a == prefix || strings.HasPrefix(a, prefix+"=") {
			return true
		}
	}
	return false
}

func flagString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return "", fmt.Errorf("invalid flag number")
		}
		if t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case json.Number:
		return t.String(), nil
	default:
		return "", fmt.Errorf("unsupported flag value type %T", v)
	}
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

// rejectMeta blocks shell metacharacters. Dispatch is in-process, not sh -c.
func rejectMeta(kind, s string) error {
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, ";|&$`\\<>(){}[]!#*?~'\"") {
		return fmt.Errorf("rejected shell metacharacters in %s", kind)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return fmt.Errorf("rejected shell metacharacters in %s", kind)
		}
	}
	return nil
}

func toolOK(text string) toolResult {
	return toolResult{
		Content: []toolContent{{Type: "text", Text: text}},
		IsError: false,
	}
}

func toolErr(err error) toolResult {
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	return toolResult{
		Content: []toolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

func hasID(id json.RawMessage) bool {
	s := bytes.TrimSpace(id)
	return len(s) > 0 && !bytes.Equal(s, []byte("null"))
}

func writeResult(enc *json.Encoder, id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return enc.Encode(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: "2.0", ID: id, Result: raw})
}

func writeError(enc *json.Encoder, id json.RawMessage, code int, msg string) error {
	if !hasID(id) {
		id = []byte("null")
	}
	return enc.Encode(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   rpcErrorBody    `json:"error"`
	}{JSONRPC: "2.0", ID: id, Error: rpcErrorBody{Code: code, Message: msg}})
}
