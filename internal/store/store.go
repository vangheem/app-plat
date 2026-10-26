package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

type User struct {
	ID                string
	Email             string
	Name              string
	Role              string
	LLMRequiresReview bool
	LLMCapUSD         float64
	CreatedAt         time.Time
}

type APIKey struct {
	ID        string
	UserID    string
	Name      string
	Prefix    string
	KeyHash   string
	CreatedAt time.Time
}

type App struct {
	ID           string
	OwnerID      string
	OwnerEmail   string
	Name         string
	Slug         string
	Port         int
	Persistence  []string
	Status       string
	Image        string
	DetectedType string
	PublicURL    string
	BuildLog     string
	Error        string
	SourcePath   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type EnvVar struct {
	Key    string
	Value  string
	Secret bool
}

type Resources struct {
	AppID          string
	PGUser         string
	PGPassword     string
	PGSchema       string
	PGDatabase     string
	BlobBucket     string
	BlobAccessKey  string
	BlobSecretKey  string
	BlobQuotaBytes int64
	BlobUsedBytes  int64
	KVUsername     string
	KVPassword     string
	KVPrefix       string
	LLMAPIKey      string
	LLMEnabled     bool
}

type Build struct {
	ID        string
	AppID     string
	Status    string
	Image     string
	Log       string
	Error     string
	CreatedAt time.Time
}

type LLMModel struct {
	ID              string
	Name            string
	Enabled         bool
	ContextLength   int
	PromptPrice     string
	CompletionPrice string
}

type Grant struct {
	ID             string
	UserID         string
	UserEmail      string
	AppID          string
	AppSlug        string
	Status         string
	SpendingCapUSD float64
	Models         []string
	Note           string
	CreatedAt      time.Time
	DecidedAt      *time.Time
}

type UsageRow struct {
	UserID           string
	UserEmail        string
	AppID            string
	AppSlug          string
	Model            string
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	Requests         int
}

type Store struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, schemaSQL)
	return err
}

func (s *Store) UpsertUser(ctx context.Context, u User) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO users (id, email, name, role, llm_requires_review, llm_cap_usd)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET email=EXCLUDED.email, name=EXCLUDED.name`,
		u.ID, u.Email, u.Name, u.Role, u.LLMRequiresReview, u.LLMCapUSD)
	return err
}

func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, userSelect+` WHERE id=$1`, id))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, userSelect+` WHERE email=$1`, email))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, userSelect+` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if out == nil {
		out = []User{}
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, id string, role *string, review *bool, name *string, cap *float64) (User, error) {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return u, err
	}
	if role != nil {
		u.Role = *role
	}
	if review != nil {
		u.LLMRequiresReview = *review
	}
	if name != nil {
		u.Name = *name
	}
	if cap != nil {
		u.LLMCapUSD = *cap
	}
	_, err = s.Pool.Exec(ctx, `
		UPDATE users SET role=$2, llm_requires_review=$3, name=$4, llm_cap_usd=$5 WHERE id=$1`,
		id, u.Role, u.LLMRequiresReview, u.Name, u.LLMCapUSD)
	return u, err
}

const userSelect = `SELECT id, email, name, role, llm_requires_review, llm_cap_usd::float8, created_at FROM users`

type rower interface {
	Scan(dest ...any) error
}

func scanUser(row rower) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.LLMRequiresReview, &u.LLMCapUSD, &u.CreatedAt)
	return u, err
}

func (s *Store) InsertAPIKey(ctx context.Context, k APIKey) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO api_keys (id, user_id, name, prefix, key_hash) VALUES ($1,$2,$3,$4,$5)`,
		k.ID, k.UserID, k.Name, k.Prefix, k.KeyHash)
	return err
}

func (s *Store) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, user_id, name, prefix, key_hash, created_at FROM api_keys
		WHERE user_id=$1 AND revoked_at IS NULL ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.KeyHash, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if out == nil {
		out = []APIKey{}
	}
	return out, rows.Err()
}

func (s *Store) RevokeAPIKey(ctx context.Context, userID, id string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) LookupAPIKey(ctx context.Context, prefix, hash string) (User, error) {
	var userID string
	err := s.Pool.QueryRow(ctx, `
		SELECT user_id FROM api_keys WHERE prefix=$1 AND key_hash=$2 AND revoked_at IS NULL`, prefix, hash).Scan(&userID)
	if err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, userID)
}

func (s *Store) CreateApp(ctx context.Context, a App) error {
	pers, err := json.Marshal(a.Persistence)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO apps (id, owner_id, name, slug, port, persistence, status, public_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		a.ID, a.OwnerID, a.Name, a.Slug, a.Port, pers, a.Status, a.PublicURL)
	return err
}

func (s *Store) SlugExists(ctx context.Context, slug string) (bool, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM apps WHERE slug=$1`, slug).Scan(&n)
	return n > 0, err
}

const appSelect = `
SELECT a.id, a.owner_id, COALESCE(u.email,''), a.name, a.slug, a.port, a.persistence::text,
       a.status, a.image, a.detected_type, a.public_url, a.build_log, a.error, a.source_path,
       a.created_at, a.updated_at
FROM apps a LEFT JOIN users u ON u.id=a.owner_id`

func (s *Store) GetApp(ctx context.Context, id string) (App, error) {
	return scanApp(s.Pool.QueryRow(ctx, appSelect+` WHERE a.id=$1`, id))
}

func (s *Store) GetAppBySlug(ctx context.Context, slug string) (App, error) {
	return scanApp(s.Pool.QueryRow(ctx, appSelect+` WHERE a.slug=$1`, slug))
}

func (s *Store) ListApps(ctx context.Context, ownerID string) ([]App, error) {
	q := appSelect + ` ORDER BY a.created_at DESC`
	args := []any{}
	if ownerID != "" {
		q = appSelect + ` WHERE a.owner_id=$1 ORDER BY a.created_at DESC`
		args = append(args, ownerID)
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if out == nil {
		out = []App{}
	}
	return out, rows.Err()
}

func scanApp(row rower) (App, error) {
	var a App
	var pers string
	err := row.Scan(&a.ID, &a.OwnerID, &a.OwnerEmail, &a.Name, &a.Slug, &a.Port, &pers,
		&a.Status, &a.Image, &a.DetectedType, &a.PublicURL, &a.BuildLog, &a.Error, &a.SourcePath,
		&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	if pers != "" && pers != "null" {
		if err := json.Unmarshal([]byte(pers), &a.Persistence); err != nil {
			return a, err
		}
	}
	if a.Persistence == nil {
		a.Persistence = []string{}
	}
	return a, nil
}

func (s *Store) UpdateApp(ctx context.Context, a App) error {
	pers, err := json.Marshal(a.Persistence)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `
		UPDATE apps SET name=$2, port=$3, persistence=$4, status=$5, image=$6, detected_type=$7,
			public_url=$8, build_log=$9, error=$10, source_path=$11, updated_at=now()
		WHERE id=$1`,
		a.ID, a.Name, a.Port, pers, a.Status, a.Image, a.DetectedType, a.PublicURL, a.BuildLog, a.Error, a.SourcePath)
	return err
}

func (s *Store) DeleteApp(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM apps WHERE id=$1`, id)
	return err
}

func (s *Store) GetEnv(ctx context.Context, appID string) ([]EnvVar, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key, value, secret FROM app_env WHERE app_id=$1 ORDER BY key`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnvVar
	for rows.Next() {
		var v EnvVar
		if err := rows.Scan(&v.Key, &v.Value, &v.Secret); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if out == nil {
		out = []EnvVar{}
	}
	return out, rows.Err()
}

func (s *Store) PutEnv(ctx context.Context, appID string, vars []EnvVar) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM app_env WHERE app_id=$1`, appID); err != nil {
		return err
	}
	for _, v := range vars {
		if _, err := tx.Exec(ctx, `INSERT INTO app_env (app_id, key, value, secret) VALUES ($1,$2,$3,$4)`, appID, v.Key, v.Value, v.Secret); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) GetResources(ctx context.Context, appID string) (Resources, error) {
	var r Resources
	err := s.Pool.QueryRow(ctx, `
		SELECT app_id, COALESCE(pg_user,''), COALESCE(pg_password,''), COALESCE(pg_schema,''), COALESCE(pg_database,''),
		       COALESCE(blob_bucket,''), COALESCE(blob_access_key,''), COALESCE(blob_secret_key,''),
		       blob_quota_bytes, blob_used_bytes,
		       COALESCE(kv_username,''), COALESCE(kv_password,''), COALESCE(kv_prefix,''),
		       COALESCE(llm_api_key,''), llm_enabled
		FROM app_resources WHERE app_id=$1`, appID).Scan(
		&r.AppID, &r.PGUser, &r.PGPassword, &r.PGSchema, &r.PGDatabase,
		&r.BlobBucket, &r.BlobAccessKey, &r.BlobSecretKey, &r.BlobQuotaBytes, &r.BlobUsedBytes,
		&r.KVUsername, &r.KVPassword, &r.KVPrefix, &r.LLMAPIKey, &r.LLMEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resources{AppID: appID, BlobQuotaBytes: 1 << 30}, nil
	}
	return r, err
}

func (s *Store) SaveResources(ctx context.Context, r Resources) error {
	if r.BlobQuotaBytes == 0 {
		r.BlobQuotaBytes = 1 << 30
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO app_resources (app_id, pg_user, pg_password, pg_schema, pg_database,
			blob_bucket, blob_access_key, blob_secret_key, blob_quota_bytes, blob_used_bytes,
			kv_username, kv_password, kv_prefix, llm_api_key, llm_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (app_id) DO UPDATE SET
			pg_user=EXCLUDED.pg_user, pg_password=EXCLUDED.pg_password, pg_schema=EXCLUDED.pg_schema, pg_database=EXCLUDED.pg_database,
			blob_bucket=EXCLUDED.blob_bucket, blob_access_key=EXCLUDED.blob_access_key, blob_secret_key=EXCLUDED.blob_secret_key,
			blob_quota_bytes=EXCLUDED.blob_quota_bytes, blob_used_bytes=EXCLUDED.blob_used_bytes,
			kv_username=EXCLUDED.kv_username, kv_password=EXCLUDED.kv_password, kv_prefix=EXCLUDED.kv_prefix,
			llm_api_key=EXCLUDED.llm_api_key, llm_enabled=EXCLUDED.llm_enabled`,
		r.AppID, r.PGUser, r.PGPassword, r.PGSchema, r.PGDatabase,
		r.BlobBucket, r.BlobAccessKey, r.BlobSecretKey, r.BlobQuotaBytes, r.BlobUsedBytes,
		r.KVUsername, r.KVPassword, r.KVPrefix, r.LLMAPIKey, r.LLMEnabled)
	return err
}

func (s *Store) AddBlobUsage(ctx context.Context, accessKey string, delta int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE app_resources SET blob_used_bytes = blob_used_bytes + $2 WHERE blob_access_key=$1`, accessKey, delta)
	return err
}

func (s *Store) ResourcesByBlobKey(ctx context.Context, accessKey string) (Resources, error) {
	var appID string
	err := s.Pool.QueryRow(ctx, `SELECT app_id FROM app_resources WHERE blob_access_key=$1`, accessKey).Scan(&appID)
	if err != nil {
		return Resources{}, err
	}
	return s.GetResources(ctx, appID)
}

func (s *Store) InsertBuild(ctx context.Context, b Build) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO builds (id, app_id, status, image, log, error) VALUES ($1,$2,$3,$4,$5,$6)`,
		b.ID, b.AppID, b.Status, b.Image, b.Log, b.Error)
	return err
}

func (s *Store) ListBuilds(ctx context.Context, appID string) ([]Build, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, app_id, status, image, log, error, created_at FROM builds WHERE app_id=$1 ORDER BY created_at DESC LIMIT 20`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Build
	for rows.Next() {
		var b Build
		if err := rows.Scan(&b.ID, &b.AppID, &b.Status, &b.Image, &b.Log, &b.Error, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if out == nil {
		out = []Build{}
	}
	return out, rows.Err()
}

func (s *Store) UpsertModel(ctx context.Context, m LLMModel) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO llm_models (id, name, enabled, context_length, prompt_price, completion_price, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, context_length=EXCLUDED.context_length,
			prompt_price=EXCLUDED.prompt_price, completion_price=EXCLUDED.completion_price, updated_at=now()`,
		m.ID, m.Name, m.Enabled, m.ContextLength, m.PromptPrice, m.CompletionPrice)
	return err
}

func (s *Store) SetModelEnabled(ctx context.Context, id string, enabled bool) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE llm_models SET enabled=$2, updated_at=now() WHERE id=$1`, id, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("unknown model %s", id)
	}
	return nil
}

func (s *Store) ListModels(ctx context.Context, enabledOnly bool) ([]LLMModel, error) {
	q := `SELECT id, name, enabled, context_length, prompt_price, completion_price FROM llm_models`
	if enabledOnly {
		q += ` WHERE enabled`
	}
	q += ` ORDER BY id`
	rows, err := s.Pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LLMModel
	for rows.Next() {
		var m LLMModel
		if err := rows.Scan(&m.ID, &m.Name, &m.Enabled, &m.ContextLength, &m.PromptPrice, &m.CompletionPrice); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []LLMModel{}
	}
	return out, rows.Err()
}

func (s *Store) InsertGrant(ctx context.Context, g Grant) error {
	models, _ := json.Marshal(g.Models)
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO llm_grants (id, user_id, app_id, status, spending_cap_usd, models, note, decided_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		g.ID, g.UserID, g.AppID, g.Status, g.SpendingCapUSD, models, g.Note, g.DecidedAt)
	return err
}

func (s *Store) LatestGrant(ctx context.Context, appID string) (Grant, error) {
	rows, err := s.Pool.Query(ctx, grantSelect+` WHERE g.app_id=$1 ORDER BY g.created_at DESC LIMIT 1`, appID)
	if err != nil {
		return Grant{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Grant{}, pgx.ErrNoRows
	}
	return scanGrant(rows)
}

func (s *Store) ListGrants(ctx context.Context, userID, status string) ([]Grant, error) {
	q := grantSelect + ` WHERE 1=1`
	args := []any{}
	if userID != "" {
		args = append(args, userID)
		q += fmt.Sprintf(` AND g.user_id=$%d`, len(args))
	}
	if status != "" {
		args = append(args, status)
		q += fmt.Sprintf(` AND g.status=$%d`, len(args))
	}
	q += ` ORDER BY g.created_at DESC LIMIT 200`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if out == nil {
		out = []Grant{}
	}
	return out, rows.Err()
}

func (s *Store) GetGrant(ctx context.Context, id string) (Grant, error) {
	rows, err := s.Pool.Query(ctx, grantSelect+` WHERE g.id=$1`, id)
	if err != nil {
		return Grant{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Grant{}, pgx.ErrNoRows
	}
	return scanGrant(rows)
}

const grantSelect = `
SELECT g.id, g.user_id, COALESCE(u.email,''), g.app_id, COALESCE(a.slug,''), g.status,
       g.spending_cap_usd::float8, g.models::text, g.note, g.created_at, g.decided_at
FROM llm_grants g
LEFT JOIN users u ON u.id=g.user_id
LEFT JOIN apps a ON a.id=g.app_id`

func scanGrant(row rower) (Grant, error) {
	var g Grant
	var models string
	err := row.Scan(&g.ID, &g.UserID, &g.UserEmail, &g.AppID, &g.AppSlug, &g.Status,
		&g.SpendingCapUSD, &models, &g.Note, &g.CreatedAt, &g.DecidedAt)
	if err != nil {
		return g, err
	}
	if models != "" && models != "null" {
		_ = json.Unmarshal([]byte(models), &g.Models)
	}
	if g.Models == nil {
		g.Models = []string{}
	}
	return g, nil
}

func (s *Store) SetGrantStatus(ctx context.Context, id, status, note string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE llm_grants SET status=$2, note=$3, decided_at=now() WHERE id=$1`, id, status, note)
	return err
}

func (s *Store) RecordUsage(ctx context.Context, userID, appID, model string, prompt, completion int, cost float64) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO llm_usage (user_id, app_id, model, prompt_tokens, completion_tokens, cost_usd)
		VALUES ($1,$2,$3,$4,$5,$6)`, userID, appID, model, prompt, completion, cost)
	return err
}

func (s *Store) MonthSpend(ctx context.Context, userID, appID string) (float64, error) {
	q := `SELECT COALESCE(sum(cost_usd),0)::float8 FROM llm_usage WHERE created_at >= date_trunc('month', now())`
	args := []any{}
	if userID != "" {
		args = append(args, userID)
		q += fmt.Sprintf(` AND user_id=$%d`, len(args))
	}
	if appID != "" {
		args = append(args, appID)
		q += fmt.Sprintf(` AND app_id=$%d`, len(args))
	}
	var n float64
	err := s.Pool.QueryRow(ctx, q, args...).Scan(&n)
	return n, err
}

func (s *Store) UsageSummary(ctx context.Context, userID, appID string) ([]UsageRow, error) {
	q := `
		SELECT u.user_id, COALESCE(us.email,''), u.app_id, COALESCE(a.slug,''), u.model,
		       COALESCE(sum(u.prompt_tokens),0), COALESCE(sum(u.completion_tokens),0),
		       COALESCE(sum(u.cost_usd),0)::float8, count(*)
		FROM llm_usage u
		LEFT JOIN users us ON us.id=u.user_id
		LEFT JOIN apps a ON a.id=u.app_id
		WHERE 1=1`
	args := []any{}
	if userID != "" {
		args = append(args, userID)
		q += fmt.Sprintf(` AND u.user_id=$%d`, len(args))
	}
	if appID != "" {
		args = append(args, appID)
		q += fmt.Sprintf(` AND u.app_id=$%d`, len(args))
	}
	q += ` GROUP BY u.user_id, us.email, u.app_id, a.slug, u.model ORDER BY 8 DESC`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.UserID, &r.UserEmail, &r.AppID, &r.AppSlug, &r.Model, &r.PromptTokens, &r.CompletionTokens, &r.CostUSD, &r.Requests); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []UsageRow{}
	}
	return out, rows.Err()
}

func (s *Store) ResourcesByLLMKey(ctx context.Context, key string) (Resources, App, error) {
	var appID string
	err := s.Pool.QueryRow(ctx, `SELECT app_id FROM app_resources WHERE llm_api_key=$1`, key).Scan(&appID)
	if err != nil {
		return Resources{}, App{}, err
	}
	res, err := s.GetResources(ctx, appID)
	if err != nil {
		return res, App{}, err
	}
	app, err := s.GetApp(ctx, appID)
	return res, app, err
}
