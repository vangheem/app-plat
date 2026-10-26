package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr       string
	Namespace        string
	DatabaseURL      string
	KratosPublicURL  string
	KratosAdminURL   string
	HydraAdminURL    string
	HydraPublicURL   string
	AdminEmail       string
	AdminPassword    string
	DataDir          string
	AppsDomain       string
	UIDomain         string
	GatewayPort      int
	ImagePullPolicy  string
	Registry         string
	BlobEndpoint     string
	BlobAccessKey    string
	BlobSecretKey    string
	BlobPublicEndpoint string
	BlobRegion       string
	KVAddr           string
	KVPassword       string
	PGAppHost        string
	PGAppPort        string
	PGDatabase       string
	InternalToken    string
	OpenRouterAPIKey string
	OpenRouterURL    string
	InCluster        bool
}

func Load() Config {
	port := 30080
	if v := os.Getenv("APP_PLAT_GATEWAY_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	return Config{
		ListenAddr:         env("LISTEN_ADDR", ":8080"),
		Namespace:          env("APP_PLAT_NAMESPACE", "app-plat"),
		DatabaseURL:        env("APP_PLAT_DATABASE_URL", "postgres://appplat:appplat@postgres:5432/appplat?sslmode=disable"),
		KratosPublicURL:    strings.TrimRight(env("KRATOS_PUBLIC_URL", "http://kratos:4433"), "/"),
		KratosAdminURL:     strings.TrimRight(env("KRATOS_ADMIN_URL", "http://kratos:4434"), "/"),
		HydraAdminURL:      strings.TrimRight(env("HYDRA_ADMIN_URL", "http://hydra:4445"), "/"),
		HydraPublicURL:     strings.TrimRight(env("HYDRA_PUBLIC_URL", "http://hydra:4444"), "/"),
		AdminEmail:         env("APP_PLAT_ADMIN_EMAIL", "admin@app-plat.local"),
		AdminPassword:      env("APP_PLAT_ADMIN_PASSWORD", "app-plat-admin"),
		DataDir:            env("APP_PLAT_DATA_DIR", "/var/lib/app-plat"),
		AppsDomain:         env("APP_PLAT_APPS_DOMAIN", "apps.127.0.0.1.nip.io"),
		UIDomain:           env("APP_PLAT_UI_DOMAIN", "127.0.0.1.nip.io"),
		GatewayPort:        port,
		ImagePullPolicy:    env("APP_PLAT_IMAGE_PULL_POLICY", "Never"),
		Registry:           env("APP_PLAT_REGISTRY", "registry:5000"),
		BlobEndpoint:       env("APP_PLAT_BLOB_ENDPOINT", "http://rustfs:9000"),
		BlobAccessKey:      env("APP_PLAT_BLOB_ACCESS_KEY", "rustfsadmin"),
		BlobSecretKey:      env("APP_PLAT_BLOB_SECRET_KEY", "rustfsadmin"),
		BlobPublicEndpoint: env("APP_PLAT_BLOB_PUBLIC_ENDPOINT", "http://blob-gateway:9000"),
		BlobRegion:         env("APP_PLAT_BLOB_REGION", "us-east-1"),
		KVAddr:             env("APP_PLAT_KV_ADDR", "valkey:6379"),
		KVPassword:         env("APP_PLAT_KV_PASSWORD", "appplat"),
		PGAppHost:          env("APP_PLAT_POSTGRES_APP_HOST", "pgbouncer"),
		PGAppPort:          env("APP_PLAT_POSTGRES_APP_PORT", "5432"),
		PGDatabase:         env("APP_PLAT_POSTGRES_DATABASE", "appplat"),
		InternalToken:      env("APP_PLAT_INTERNAL_TOKEN", "app-plat-internal"),
		OpenRouterAPIKey:   os.Getenv("OPENROUTER_API_KEY"),
		OpenRouterURL:      strings.TrimRight(env("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"), "/"),
		InCluster:          os.Getenv("KUBERNETES_SERVICE_HOST") != "",
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func (c Config) PublicURL(slug string) string {
	host := slug + "." + c.AppsDomain
	if c.GatewayPort == 80 || c.GatewayPort == 0 {
		return "http://" + host + "/"
	}
	return "http://" + host + ":" + strconv.Itoa(c.GatewayPort) + "/"
}
