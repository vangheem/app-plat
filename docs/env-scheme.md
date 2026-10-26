# Environment scheme

Every deployed app receives these variables. Names are stable. User-declared env vars and secrets are added beside them.

| Variable | Always | Meaning |
|---|---|---|
| `PORT` | yes | Port the process must listen on |
| `APP_PLAT_PORT` | yes | Same value as `PORT` |
| `APP_PLAT_APP_NAME` | yes | App slug |
| `APP_PLAT_PUBLIC_URL` | yes | External URL via the Istio gateway |

### PostgreSQL (`persistence: postgresql`)

A dedicated role and schema are created. The role's `search_path` is the schema, so unqualified SQL stays inside the app. Traffic goes through PgBouncer (transaction pooling, `auth_query`, so new roles work without a reload).

Slug `my-app` becomes schema and role `app_my_app`.

| Variable | Example |
|---|---|
| `APP_PLAT_POSTGRES_HOST` | `pgbouncer` |
| `APP_PLAT_POSTGRES_PORT` | `5432` |
| `APP_PLAT_POSTGRES_USER` | `app_my_app` |
| `APP_PLAT_POSTGRES_PASSWORD` | generated |
| `APP_PLAT_POSTGRES_DATABASE` | `appplat` |
| `APP_PLAT_POSTGRES_SCHEMA` | `app_my_app` |
| `APP_PLAT_POSTGRES_URL` | `postgres://app_my_app:PASSWORD@pgbouncer:5432/appplat?sslmode=disable` |

Do not point apps at the Postgres primary. The primary is only for the platform, Kratos, Hydra, and PgBouncer.

### Blob (`persistence: blob`)

RustFS is the storage engine. Apps never receive RustFS admin credentials. They receive a per-app access key accepted by `blob-gateway`, which enforces the bucket and a byte quota (default 1 GiB).

| Variable | Example |
|---|---|
| `APP_PLAT_BLOB_ENDPOINT` | `http://blob-gateway:9000` |
| `APP_PLAT_BLOB_BUCKET` | `app-my-app` |
| `APP_PLAT_BLOB_ACCESS_KEY` | generated |
| `APP_PLAT_BLOB_SECRET_KEY` | generated |
| `APP_PLAT_BLOB_REGION` | `us-east-1` |
| `APP_PLAT_BLOB_USE_PATH_STYLE` | `true` |

Use path-style S3. The bucket already exists; do not create buckets.

### Key/value (`persistence: keyvalue`)

Valkey ACL user restricted to one key prefix and one pub/sub channel prefix.

| Variable | Example |
|---|---|
| `APP_PLAT_KV_HOST` | `valkey` |
| `APP_PLAT_KV_PORT` | `6379` |
| `APP_PLAT_KV_USERNAME` | `app_my_app` |
| `APP_PLAT_KV_PASSWORD` | generated |
| `APP_PLAT_KV_PREFIX` | `app:my-app:` |
| `APP_PLAT_KV_URL` | `redis://app_my_app:PASSWORD@valkey:6379/0` |

Every key must start with `APP_PLAT_KV_PREFIX`. Other keys are denied by ACL.

### LLM (only after an approved grant)

| Variable | Example |
|---|---|
| `APP_PLAT_LLM_BASE_URL` | `http://llm-gateway:8083/v1` |
| `APP_PLAT_LLM_API_KEY` | `llmk_...` |

The base URL is OpenAI-compatible (`/chat/completions`, `/models`).

## External providers

Set these on the control plane. `make deps-up` installs the in-cluster defaults; skip it when mode is `external` and point the URLs at your own endpoints. The same allocation scheme is used either way.

```
APP_PLAT_POSTGRES_MODE=internal|external
APP_PLAT_POSTGRES_ADMIN_URL=postgres://appplat:appplat@postgres:5432/appplat?sslmode=disable
APP_PLAT_POSTGRES_APP_HOST=pgbouncer
APP_PLAT_POSTGRES_APP_PORT=5432
APP_PLAT_POSTGRES_DATABASE=appplat

APP_PLAT_BLOB_MODE=internal|external
APP_PLAT_BLOB_ENDPOINT=http://rustfs:9000
APP_PLAT_BLOB_ACCESS_KEY=rustfsadmin
APP_PLAT_BLOB_SECRET_KEY=rustfsadmin
APP_PLAT_BLOB_PUBLIC_ENDPOINT=http://blob-gateway:9000

APP_PLAT_KV_MODE=internal|external
APP_PLAT_KV_ADDR=valkey:6379
APP_PLAT_KV_PASSWORD=appplat
```
