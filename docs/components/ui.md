# UI agent brief

Build two HTMX servers. No React, no Tailwind, no extra UI framework. Use the CSS and HTMX already vendored at `web/static/app.css` and `web/static/htmx.min.js`. Embed them with `go:embed` (copy the two files into your package if embed cannot see `../../web`, or embed via a relative path that Go accepts — `//go:embed` cannot use `..`, so copy `app.css` and `htmx.min.js` into each UI package's `static/` directory and keep them identical).

Use `github.com/app-plat/app-plat/pkg/apiclient` and `pkg/api`. Do not invent endpoints.

## Shared behavior

- Env `CONTROL_PLANE_URL` default `http://control-plane:8080`.
- Env `LISTEN_ADDR` default `:8081` for admin and `:8082` for user.
- Cookie `app_plat_session`, httpOnly, SameSite=Lax, path=/.
- Unauthenticated pages redirect to `/login`.
- Render errors as a `.flash` div. Do not panic on API errors.
- Pages use the `.shell` layout from `web/static/app.css`: dark sidebar, paper background, copper accent.
- HTMX polls status fragments. Include `<script src="/static/htmx.min.js"></script>`.
- Serve static files at `/static/`.
- `GET /healthz` returns `ok`.

## User UI (`cmd/user`, `internal/userui`)

Routes:

- `GET /login`, `POST /login`, `GET /register`, `POST /register`, `POST /logout`
- `GET /` app list with name, status badge, public URL
- `GET /apps/new`, `POST /apps` form: name, port, persistence checkboxes (postgresql, blob, keyvalue), optional app.yaml textarea
- `GET /apps/{id}` detail: status, URL, detected type, build log (poll every 3s with HTMX), env editor, secret checkbox per row, file upload for archive, rebuild button, delete button, LLM request form
- `POST /apps/{id}/env` replaces env. Secret values already stored come back masked as empty with `secret=true`; if the submitted secret value is empty, keep the previous secret (call GetEnv first and preserve).
- `POST /apps/{id}/source` multipart field `archive`
- `POST /apps/{id}/rebuild`
- `POST /apps/{id}/delete`
- `POST /apps/{id}/llm` fields `models` (multi) and `spending_cap_usd`. Show current grant status and whether the user requires review (`LLMPolicy`).
- `GET /settings` API keys. Create shows the plaintext key once. Revoke button.
- OAuth2 login and consent, required for Hydra:
  - `GET /oauth2/login?login_challenge=`
  - `POST /oauth2/login` accepts the challenge via `OAuthAcceptLogin` using the current session, then redirects to `redirect_to`
  - `GET /oauth2/consent?consent_challenge=`
  - `POST /oauth2/consent` accepts via `OAuthAcceptConsent`

If the user is not logged in when Hydra sends them to `/oauth2/login`, show the login form and return them to the same challenge afterward.

Admin users may use the user UI too. Do not require role=user.

## Admin UI (`cmd/admin`, `internal/adminui`)

Same login cookie flow. After login, if `user.role != "admin"`, show a forbidden page.

Routes:

- `GET /` dashboard: health components, app count, user count, pending LLM requests
- `GET /users`, `POST /users` create user (email, password, name, role, llm_requires_review)
- `POST /users/{id}` update role, name, llm_requires_review, llm_cap_usd
- `GET /apps` all apps with owner, status, URL
- `GET /apps/{id}` read-only detail plus build log
- `GET /llm` models table with enable checkboxes, sync button, pending requests with approve/deny, usage table, cap form
- `POST /llm/models/sync`, `POST /llm/models` (toggles), `POST /llm/requests/{id}` approve or deny, `POST /llm/caps`
- `GET /oauth` list clients, create client (name, redirect URIs comma-separated, scopes default `openid offline apps`), show secret once, delete
- `GET /health` raw component list

## Quality bar

- Server-rendered Go `html/template`. Escape by default.
- Forms work without HTMX; HTMX only enhances polling and a few partial swaps.
- Empty states, not blank pages.
- `gofmt` everything you write.
- Do not add modules. Stdlib plus the existing `pkg/apiclient` and `pkg/api` only.
