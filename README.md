# app-plat

A small, purpose-built application platform for getting vibe-coded apps onto Kubernetes without giving them the cluster.

Engineers upload an archive. The control plane detects the language, builds an image, allocates isolated persistence, and publishes the app through Istio. Apps are locked down by default: they can reach only the data services they asked for, and nothing else in the platform.

Everything is driven by the Makefile.

## What you get

- Ory Kratos and Hydra for users and OAuth2. No Auth0 or Okta required.
- A bootstrap admin you can use immediately.
- API keys for the CLI and for agents.
- An Istio gateway plus default-deny authorization. User apps are reachable only through the gateway.
- Postgres (schema and role per app, PgBouncer in front), RustFS (bucket per app, via a credential proxy), and Valkey (ACL key prefix per app).
- A registry for the images the platform builds.
- Admin and user consoles in HTMX.
- An OpenAI-compatible LLM gateway in front of OpenRouter, with model allow-lists, spending caps, and an approval flow.
- A CLI that also speaks MCP over stdio with two tools: `capabilities` and `execute`.

## Install

```bash
make up
make admin-credentials
```

`make up` installs Istio, the data services, Kratos, Hydra, and the platform into the `app-plat` namespace. Istio's own control plane stays in `istio-system` because Istio requires that.

Local URLs, via the ingress NodePort:

| Surface | URL |
|---|---|
| User console | http://app.127.0.0.1.nip.io:30080/ |
| Admin console | http://admin.127.0.0.1.nip.io:30080/ |
| API | http://api.127.0.0.1.nip.io:30080/ |
| OAuth2 issuer | http://oauth.127.0.0.1.nip.io:30080/ |
| An app | http://\<slug\>.apps.127.0.0.1.nip.io:30080/ |

Default admin: `admin@app-plat.local` / `app-plat-admin`.

Override the password with `APP_PLAT_ADMIN_PASSWORD` before `make up`. The OpenRouter key is read from `../goodknight/.env` (or `app-plat/.env` if you point `OPENROUTER_ENV` at it) and stored in a Kubernetes secret. It is not committed.

## Make targets

| Target | What it does |
|---|---|
| `make up` | Full install |
| `make down` | Delete the `app-plat` namespace |
| `make status` | Pods, services, and mesh policy |
| `make admin-credentials` | Print the bootstrap login |
| `make images` | Build platform images into the local Docker engine |
| `make istio-up` | Install Istio and apply mesh policy |
| `make deps-up` | Postgres, PgBouncer, Valkey, RustFS, registry |
| `make idp-up` | Kratos and Hydra |
| `make platform-up` | Control plane, gateways, consoles |
| `make e2e` | Exercise login, CLI, MCP, and both example apps |
| `make test` | Package tests |
| `make logs` | Tail the control plane |

Skip `make deps-up` when Postgres, blob, or Valkey are external. Point the control plane at them with the variables in [docs/env-scheme.md](docs/env-scheme.md).

## App contract

Put `app.yaml` at the root of the archive:

```yaml
name: My App
port: 8080
persistence:
  - postgresql
  - blob
  - keyvalue
```

If the archive has a Dockerfile, it is used. Otherwise the builder recognizes Python (uv or pip), Node, Go, and Rust and writes one. The process must listen on `PORT`.

Connection strings are injected as environment variables. The names are stable and documented in [docs/env-scheme.md](docs/env-scheme.md). A slug `my-app` gets Postgres schema `app_my_app`, blob bucket `app-my-app`, and Valkey prefix `app:my-app:`.

Users can also set plain env vars and secrets in the console. Secrets are stored by the platform and mounted as env vars. They are not shown again after save.

## LLM access

Admins sync the OpenRouter catalog and enable the models apps may use. A user requests a model list and a spending cap for one app.

If the user is marked "requires review", the grant stays pending and the app does not receive a key until an admin approves. Later changes go back to pending. If review is not required, the grant is applied immediately. The admin is created with review disabled.

Approved apps receive `APP_PLAT_LLM_BASE_URL` and `APP_PLAT_LLM_API_KEY`. The base URL is OpenAI-compatible.

## CLI and MCP

```bash
app-plat login --api-key apk_... --api-url http://api.127.0.0.1.nip.io:30080
app-plat apps list
app-plat mcp
```

`app-plat mcp` is a stdio MCP server with two tools:

- `capabilities` searches the command catalog
- `execute` runs one of those commands in-process

## Layout

```
cmd/                 control-plane, blob-gateway, admin, user, llm-gateway, app-plat
internal/            reconciler, store, consoles, gateway, CLI
deploy/              Kubernetes, Istio, Ory, Dockerfiles
examples/            python and golang apps, no Dockerfiles
docs/                architecture and the env scheme
```

## Lockdown

User-app pods are labeled `app-plat.io/role=user-app`. Istio AuthorizationPolicy denies the namespace by default. An app accepts traffic only from the ingress gateway. Its NetworkPolicy allows egress only to DNS, istiod, and the persistence services it requested, plus the LLM gateway. Postgres, RustFS, and the registry do not get sidecars; NetworkPolicy is what keeps user pods off them. Apps talk to PgBouncer, not the primary, and to the blob gateway, not RustFS.
