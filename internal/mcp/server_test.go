package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolAndExecute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APP_PLAT_API_URL", "")
	t.Setenv("APP_PLAT_API_KEY", "")

	var hits int
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer apk_test" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/v1/apps" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"id":"app_1","name":"Demo","slug":"demo","port":8080,"status":"running"}]`))
	}))
	defer apiSrv.Close()

	cfgDir := filepath.Join(home, ".app-plat")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"api_url":"` + apiSrv.URL + `","api_key":"apk_test"}` + "\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	var in bytes.Buffer
	writeMsg := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		in.Write(b)
		in.WriteByte('\n')
	}
	writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "0"}},
	})
	writeMsg(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	writeMsg(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	writeMsg(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "ping"})
	writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params":  map[string]any{"name": "capabilities", "arguments": map[string]any{"query": "upload"}},
	})
	writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "tools/call",
		"params":  map[string]any{"name": "execute", "arguments": map[string]any{"command": "apps list"}},
	})
	writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "tools/call",
		"params":  map[string]any{"name": "execute", "arguments": map[string]any{"command": "mcp"}},
	})
	writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "tools/call",
		"params":  map[string]any{"name": "execute", "arguments": map[string]any{"command": "apps list; rm -rf /"}},
	})

	var out, errBuf bytes.Buffer
	if err := Serve(&in, &out, &errBuf, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "notifications/initialized") {
		t.Fatal("responded to notification")
	}
	if errBuf.Len() == 0 || strings.Contains(errBuf.String(), "apk_test") {
		t.Fatalf("stderr %q", errBuf.String())
	}

	msgs := decodeLines(t, out.Bytes())
	if len(msgs) != 7 {
		t.Fatalf("got %d responses: %s", len(msgs), out.String())
	}
	init := resultOf(t, msgs[0])
	if init["protocolVersion"] != protocolVersion {
		t.Fatalf("protocol %#v", init["protocolVersion"])
	}
	info, _ := init["serverInfo"].(map[string]any)
	if info["name"] != "app-plat" {
		t.Fatalf("serverInfo %#v", init["serverInfo"])
	}
	caps, _ := init["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("capabilities %#v", init["capabilities"])
	}

	tools, _ := resultOf(t, msgs[1])["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools %#v", tools)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		m := tool.(map[string]any)
		names[m["name"].(string)] = true
	}
	if !names["capabilities"] || !names["execute"] {
		t.Fatalf("tool names %#v", names)
	}

	if len(resultOf(t, msgs[2])) != 0 {
		t.Fatalf("ping %#v", msgs[2])
	}
	capText := toolText(t, msgs[3])
	if !strings.Contains(capText, "apps upload") || strings.Contains(capText, "keys revoke") {
		t.Fatalf("capabilities text %q", capText)
	}
	if toolIsError(t, msgs[3]) {
		t.Fatal("capabilities isError")
	}
	listText := toolText(t, msgs[4])
	if toolIsError(t, msgs[4]) || !strings.Contains(listText, "demo") {
		t.Fatalf("execute list %q", listText)
	}
	if !toolIsError(t, msgs[5]) || !strings.Contains(toolText(t, msgs[5]), "mcp") {
		t.Fatalf("mcp rejection %#v", msgs[5])
	}
	if !toolIsError(t, msgs[6]) || !strings.Contains(toolText(t, msgs[6]), "metacharacter") {
		t.Fatalf("meta rejection %#v", msgs[6])
	}
	if hits != 1 {
		t.Fatalf("api hits %d", hits)
	}
}

func decodeLines(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	var out []map[string]any
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func resultOf(t *testing.T, msg map[string]any) map[string]any {
	t.Helper()
	res, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %#v", msg)
	}
	return res
}

func toolText(t *testing.T, msg map[string]any) string {
	t.Helper()
	content, _ := resultOf(t, msg)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content %#v", resultOf(t, msg))
	}
	item := content[0].(map[string]any)
	return item["text"].(string)
}

func toolIsError(t *testing.T, msg map[string]any) bool {
	t.Helper()
	v, ok := resultOf(t, msg)["isError"].(bool)
	if !ok {
		t.Fatalf("isError %#v", resultOf(t, msg)["isError"])
	}
	return v
}
