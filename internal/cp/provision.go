package cp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/app-plat/app-plat/internal/store"
	"github.com/app-plat/app-plat/pkg/appyaml"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
)

func (s *Server) ensurePersistence(ctx context.Context, app *store.App) error {
	res, err := s.store.GetResources(ctx, app.ID)
	if err != nil {
		return err
	}
	res.AppID = app.ID
	if appyaml.Has(app.Persistence, "postgresql") {
		if err := s.ensurePostgres(ctx, app, &res); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
	}
	if appyaml.Has(app.Persistence, "blob") {
		if err := s.ensureBlob(ctx, app, &res); err != nil {
			return fmt.Errorf("blob: %w", err)
		}
	}
	if appyaml.Has(app.Persistence, "keyvalue") {
		if err := s.ensureKV(ctx, app, &res); err != nil {
			return fmt.Errorf("keyvalue: %w", err)
		}
	}
	return s.store.SaveResources(ctx, res)
}

func (s *Server) ensurePostgres(ctx context.Context, app *store.App, res *store.Resources) error {
	name := ident(app.Slug)
	if res.PGUser == "" {
		res.PGUser = name
		res.PGSchema = name
		res.PGDatabase = s.cfg.PGDatabase
		res.PGPassword = newSecret("")
	}
	qName := quoteIdent(name)
	if _, err := s.store.Pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+qName); err != nil {
		return err
	}
	var exists bool
	if err := s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, name).Scan(&exists); err != nil {
		return err
	}
	pw := quoteLiteral(res.PGPassword)
	if !exists {
		if _, err := s.store.Pool.Exec(ctx, fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD %s`, qName, pw)); err != nil {
			return err
		}
	} else {
		if _, err := s.store.Pool.Exec(ctx, fmt.Sprintf(`ALTER ROLE %s WITH LOGIN PASSWORD %s`, qName, pw)); err != nil {
			return err
		}
	}
	stmts := []string{
		fmt.Sprintf(`ALTER ROLE %s SET search_path TO %s`, qName, qName),
		fmt.Sprintf(`GRANT CONNECT ON DATABASE %s TO %s`, quoteIdent(s.cfg.PGDatabase), qName),
		fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA %s TO %s`, qName, qName),
		fmt.Sprintf(`ALTER SCHEMA %s OWNER TO %s`, qName, qName),
	}
	for _, stmt := range stmts {
		if _, err := s.store.Pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}

func (s *Server) ensureBlob(ctx context.Context, app *store.App, res *store.Resources) error {
	if res.BlobBucket == "" {
		res.BlobBucket = "app-" + app.Slug
		res.BlobAccessKey = newSecret("bk_")
		res.BlobSecretKey = newSecret("")
		res.BlobQuotaBytes = 1 << 30
	}
	client, err := s.blobAdmin()
	if err != nil {
		return err
	}
	exists, err := client.BucketExists(ctx, res.BlobBucket)
	if err != nil {
		return err
	}
	if !exists {
		if err := client.MakeBucket(ctx, res.BlobBucket, minio.MakeBucketOptions{Region: s.cfg.BlobRegion}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) blobAdmin() (*minio.Client, error) {
	u, err := url.Parse(s.cfg.BlobEndpoint)
	if err != nil {
		return nil, err
	}
	return minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(s.cfg.BlobAccessKey, s.cfg.BlobSecretKey, ""),
		Secure: u.Scheme == "https",
		Region: s.cfg.BlobRegion,
	})
}

func (s *Server) ensureKV(ctx context.Context, app *store.App, res *store.Resources) error {
	name := ident(app.Slug)
	if res.KVUsername == "" {
		res.KVUsername = name
		res.KVPassword = newSecret("")
		res.KVPrefix = "app:" + app.Slug + ":"
	}
	rdb := redis.NewClient(&redis.Options{Addr: s.cfg.KVAddr, Password: s.cfg.KVPassword})
	defer rdb.Close()
	args := []any{"ACL", "SETUSER", res.KVUsername, "reset", "on", ">" + res.KVPassword, "~" + res.KVPrefix + "*", "resetchannels", "&" + res.KVPrefix + "*", "+@read", "+@write", "+@connection", "+ping", "+info"}
	if err := rdb.Do(ctx, args...).Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) dropPersistence(ctx context.Context, app store.App) {
	res, err := s.store.GetResources(ctx, app.ID)
	if err != nil {
		return
	}
	if res.PGSchema != "" {
		_, _ = s.store.Pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quoteIdent(res.PGSchema)+" CASCADE")
		_, _ = s.store.Pool.Exec(ctx, "DROP ROLE IF EXISTS "+quoteIdent(res.PGUser))
	}
	if res.KVUsername != "" {
		rdb := redis.NewClient(&redis.Options{Addr: s.cfg.KVAddr, Password: s.cfg.KVPassword})
		_ = rdb.Do(ctx, "ACL", "DELUSER", res.KVUsername).Err()
		rdb.Close()
	}
	if res.BlobBucket != "" {
		if client, err := s.blobAdmin(); err == nil {
			_ = client.RemoveBucket(ctx, res.BlobBucket)
		}
	}
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}
