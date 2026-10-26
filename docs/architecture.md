# app-plat

Enterprise platform for deploying vibe-coded apps into a locked-down Kubernetes namespace. One namespace (`app-plat`) holds the platform and every user app. Istio's own control plane stays in `istio-system` because Istio requires it.

## Components

| Piece | Binary / image | Role |
|---|---|---|
| Identity | Ory Kratos + Hydra | Users, sessions, OAuth2. No external IdP required. |
| Control plane | `cmd/control-plane` | API, reconciler, builds, persistence allocation |
| Blob gateway | `cmd/blob-gateway` | Per-app S3 credentials in front of RustFS |
| LLM gateway | `cmd/llm-gateway` | OpenAI-compatible proxy to OpenRouter |
| Admin UI | `cmd/admin` | HTMX admin console |
| User UI | `cmd/user` | HTMX user console |
| CLI | `cmd/app-plat` | API-key CLI plus a 2-tool stdio MCP |
| Registry | `registry:2` | Stores built images |
| Postgres + PgBouncer | in-cluster by default | Schema + role per app |
| RustFS | in-cluster by default | Blob backend |
| Valkey | in-cluster by default | Key-prefix isolation per app |

External Postgres, blob, and Valkey are supported by pointing the control plane at them and skipping `make deps-up`. See `docs/env-scheme.md`.

## Default admin

On first boot the control plane creates:

- email `admin@app-plat.local`
- password `app-plat-admin` (override with `APP_PLAT_ADMIN_PASSWORD`)
- role `admin`
- `llm_requires_review=false`

`make admin-credentials` prints this. The admin can create API keys immediately.

## Auth

- Browser UIs call `POST /api/v1/login` and store `session_token` in an httpOnly cookie named `app_plat_session`.
- CLI and agents send `Authorization: Bearer apk_...`.
- API keys are stored hashed (sha256). The plaintext is returned once.
- OAuth2 is Hydra. Admin UI can create clients. User UI implements login + consent by calling the control-plane OAuth endpoints, which proxy Hydra admin.

## App contract

Users upload an archive (`.tar.gz`, `.tgz`, or `.zip`). If `app.yaml` is at the archive root it is applied.

```yaml
name: My App
port: 8080
persistence:
  - postgresql
  - blob
  - keyvalue
```

If the archive contains a `Dockerfile`, it is used. Otherwise the builder detects:

- `Cargo.toml` → rust
- `go.mod` → golang
- `package.json` → node
- `uv.lock` or `pyproject.toml` → python (uv)
- `requirements.txt` → python (pip)

Anything else fails until the user supplies a Dockerfile.

Generated apps listen on `PORT` (also injected as `APP_PLAT_PORT`).

## Reconciliation

The control plane loops every few seconds:

1. Provision requested persistence (idempotent).
2. If a source archive is present and status is `building`, build via the host Docker socket, tag `app-plat/<slug>:<unix>`, save/push the image into the in-cluster registry, and set the deployment image.
3. Apply Deployment, Service, Secret, ServiceAccount, Istio VirtualService, AuthorizationPolicy, and NetworkPolicy.
4. User apps are not exposed except through the Istio gateway.

Status values: `created`, `building`, `deploying`, `running`, `failed`, `deleting`.

## Lockdown

- User-app pods carry `app-plat.io/role=user-app` and `app-plat.io/slug=<slug>`.
- Istio sidecar injection is on for HTTP platform services and user apps.
- Data stores (postgres, pgbouncer, valkey, rustfs, registry) do **not** get sidecars.
- Namespace AuthorizationPolicy denies by default. Explicit allows exist for platform traffic and for the ingress gateway to reach user apps.
- NetworkPolicy on user apps allows egress only to DNS, istiod, pgbouncer, valkey, blob-gateway, and llm-gateway.
- Postgres accepts connections only from pgbouncer, kratos, hydra, and the control plane.
- RustFS accepts connections only from the blob gateway and the control plane.

## Env injection

Documented in `docs/env-scheme.md`. Connection strings are predictable from the app slug. User-configured env vars and secrets are merged into the same Secret. Platform keys win over user keys if they collide.

## LLM

- Admins sync the OpenRouter catalog and enable models.
- Users request LLM access per app (models + spending cap).
- If `users.llm_requires_review` is true, the grant stays `pending` and the app is not given a key until an admin approves. Later allocation changes also return to `pending` and disable access until approved.
- If review is not required, the grant is approved immediately.
- The app receives `APP_PLAT_LLM_BASE_URL` and `APP_PLAT_LLM_API_KEY` only when the latest grant is `approved`.
- The gateway checks model allow-list, grant, and monthly spend, proxies to OpenRouter, and reports usage.

## File ownership

Do not edit files outside your assignment. Do not edit `Makefile`, `go.mod`, `pkg/`, `deploy/`, or `internal/cp`.

- UI agent: `cmd/admin`, `cmd/user`, `internal/adminui`, `internal/userui`
- Edge agent: `cmd/llm-gateway`, `internal/llmgateway`, `cmd/app-plat`, `internal/cli`, `internal/mcp`, `examples`
