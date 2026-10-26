# Edge agent brief

Build the LLM gateway, the CLI (including MCP), and two example apps. Do not edit Makefile, go.mod, pkg/, deploy/, or internal/cp.

Allowed imports already expected in the module: stdlib, `github.com/app-plat/app-plat/pkg/api`, `github.com/app-plat/app-plat/pkg/apiclient`, `github.com/app-plat/app-plat/pkg/appyaml`, `gopkg.in/yaml.v3`. Do not import Cobra or an MCP SDK. Implement flags and JSON-RPC yourself.

## LLM gateway

`cmd/llm-gateway` and `internal/llmgateway`.

Listen on `LISTEN_ADDR` default `:8083`.

Env:

- `CONTROL_PLANE_URL` default `http://control-plane:8080`
- `INTERNAL_TOKEN` required, sent as `X-Internal-Token`
- `OPENROUTER_API_KEY` required for proxied calls
- `OPENROUTER_BASE_URL` default `https://openrouter.ai/api/v1`

Endpoints:

- `GET /healthz` → `ok`
- `GET /v1/models` bearer `llmk_...` → OpenAI model list containing only models the control plane authorizes for that key
- `POST /v1/chat/completions` bearer `llmk_...` → proxy to OpenRouter

Before proxying, `POST {CONTROL_PLANE_URL}/internal/llm/authorize` with header `X-Internal-Token` and body:

```json
{"api_key":"llmk_...","model":"openai/gpt-4o-mini"}
```

Response:

```json
{"allowed":true,"user_id":"...","app_id":"...","reason":""}
```

If `allowed` is false, return OpenAI-style error 403 with `reason`.

Proxy the JSON body unchanged to `{OPENROUTER_BASE_URL}/chat/completions` with `Authorization: Bearer $OPENROUTER_API_KEY` and `Content-Type: application/json`. Stream is not required. Buffer the response.

After a 200, report usage with `POST {CONTROL_PLANE_URL}/internal/llm/usage`:

```json
{"api_key":"llmk_...","model":"...","prompt_tokens":0,"completion_tokens":0,"cost_usd":0}
```

Read token counts from the OpenAI `usage` object. Read cost from the `x-openrouter-cost` response header if present, otherwise 0.

`GET /v1/models` calls `POST /internal/llm/models` with the same internal token and body `{"api_key":"llmk_..."}`, which returns `{"models":[{"id":"...","name":"..."}]}`. Render that as an OpenAI list.

Do not log API keys.

## CLI

`cmd/app-plat` main, logic in `internal/cli` and `internal/mcp`.

Config file `~/.app-plat/config.json`: `{"api_url":"http://127.0.0.1:30080","api_key":"apk_..."}`. Flag `--api-url` and env `APP_PLAT_API_URL` / `APP_PLAT_API_KEY` override.

Commands (print JSON unless `--text` is passed, except `mcp`):

- `app-plat login --api-key apk_... [--api-url URL]` saves config
- `app-plat me`
- `app-plat keys create --name NAME`
- `app-plat keys list`
- `app-plat keys revoke ID`
- `app-plat apps list`
- `app-plat apps create --name NAME [--port 8080] [--persistence postgresql,blob,keyvalue] [--file app.yaml]`
- `app-plat apps get SLUG_OR_ID`
- `app-plat apps upload SLUG_OR_ID ARCHIVE`
- `app-plat apps env set SLUG_OR_ID KEY=VALUE [--secret]`
- `app-plat apps env list SLUG_OR_ID`
- `app-plat apps delete SLUG_OR_ID`
- `app-plat llm request SLUG_OR_ID --models a,b --cap 5`
- `app-plat mcp` stdio MCP server. This is the important one.

Resolve slug or id by listing apps and matching `slug` or `id`.

### MCP

Speak MCP JSON-RPC 2.0 on stdin/stdout. Logs go to stderr only.

Handle:

- `initialize` → protocolVersion `2024-11-05`, capabilities `{tools:{}}`, serverInfo name `app-plat`
- `notifications/initialized` → no response
- `tools/list`
- `tools/call`
- `ping`

Exactly two tools:

1. `capabilities` — input `{"query":"optional substring"}`. Returns a text catalog of every CLI command above (name, args, one-line description), filtered by query. This is the help menu.
2. `execute` — input `{"command":"apps list","args":["optional","positional"],"flags":{"name":"..."}}`. Runs that command using the saved API key and returns the stdout text. Reject `mcp` as a command so it cannot recurse. Reject shell metacharacters; this is an in-process dispatch, not `sh -c`.

A tool result is:

```json
{"content":[{"type":"text","text":"..."}],"isError":false}
```

## Example apps

`examples/python-app` and `examples/golang-app`.

Each has `app.yaml`:

```yaml
name: Python Demo
port: 8080
persistence:
  - postgresql
  - blob
  - keyvalue
```

(golang app name `Golang Demo`.)

Each serves:

- `GET /healthz` → 200 `ok`
- `GET /` → plain text or simple HTML reporting connectivity for postgres, blob, and kv. Read the env vars in `docs/env-scheme.md`. Create a table `demo` if missing, insert a row, read it back. Put and get an object `demo.txt`. Set and get `{PREFIX}demo`. If `APP_PLAT_LLM_BASE_URL` is unset, say LLM is not configured. Do not fail the page if one backend errors; show the error string.
- Bind `:$PORT`, default 8080.

Python: `requirements.txt` with `psycopg2-binary`, `boto3`, `redis`. Entry file `app.py`. No Dockerfile (the platform must generate one).

Go: `go.mod` module `example.com/golang-demo`, require `github.com/jackc/pgx/v5`, `github.com/minio/minio-go/v7`, `github.com/redis/go-redis/v9`. Entry `main.go`. No Dockerfile.

Do not run `go mod tidy` in the example if it would rewrite the platform `go.mod`. The example has its own `go.mod`. Run tidy inside `examples/golang-app` only.

`gofmt` Go sources.
