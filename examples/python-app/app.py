#!/usr/bin/env python3
"""Demo app. Reports postgres, blob, and key/value connectivity from app-plat env vars."""

import os
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, TimeoutError
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import boto3
import psycopg2
import redis
from botocore.config import Config


def main():
    raw = os.environ.get("PORT", "8080") or "8080"
    port = int(raw)
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path != "/" and path.endswith("/"):
            path = path.rstrip("/")
        if path == "/healthz":
            self._write(200, "ok")
            return
        if path == "/":
            self._write(200, report())
            return
        self._write(404, "not found")

    def _write(self, status, body):
        data = body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def report():
    checks = {
        "postgres": check_postgres,
        "blob": check_blob,
        "kv": check_kv,
        "llm": check_llm,
    }
    results = {}
    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = {name: pool.submit(fn) for name, fn in checks.items()}
        for name, fut in futures.items():
            try:
                results[name] = fut.result(timeout=8)
            except TimeoutError:
                results[name] = "error: timed out"
            except Exception as exc:
                results[name] = "error: " + redact(str(exc))
    app = os.environ.get("APP_PLAT_APP_NAME", "") or "unset"
    lines = [
        "Python Demo",
        "app: " + app,
        "postgres: " + results["postgres"],
        "blob: " + results["blob"],
        "kv: " + results["kv"],
        "llm: " + results["llm"],
    ]
    return "\n".join(lines) + "\n"


def check_postgres():
    dsn = os.environ.get("APP_PLAT_POSTGRES_URL", "").strip()
    if not dsn:
        dsn = postgres_dsn_from_parts()
    if not dsn:
        return "error: postgres is not configured"
    conn = psycopg2.connect(dsn, connect_timeout=3)
    conn.autocommit = True
    try:
        with conn.cursor() as cur:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS demo (
                    id BIGSERIAL PRIMARY KEY,
                    message TEXT NOT NULL,
                    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
                )
                """
            )
            cur.execute(
                "INSERT INTO demo (message) VALUES (%s) RETURNING id, message",
                ("hello from python",),
            )
            row = cur.fetchone()
            return f"ok id={row[0]} message={row[1]}"
    finally:
        conn.close()


def postgres_dsn_from_parts():
    host = os.environ.get("APP_PLAT_POSTGRES_HOST", "")
    port = os.environ.get("APP_PLAT_POSTGRES_PORT", "")
    user = os.environ.get("APP_PLAT_POSTGRES_USER", "")
    password = os.environ.get("APP_PLAT_POSTGRES_PASSWORD", "")
    database = os.environ.get("APP_PLAT_POSTGRES_DATABASE", "")
    if not (host and port and user and database):
        return ""
    from urllib.parse import quote

    return (
        f"postgres://{quote(user)}:{quote(password)}@{host}:{port}/{database}?sslmode=disable"
    )


def check_blob():
    endpoint = os.environ.get("APP_PLAT_BLOB_ENDPOINT", "").strip()
    bucket = os.environ.get("APP_PLAT_BLOB_BUCKET", "").strip()
    access = os.environ.get("APP_PLAT_BLOB_ACCESS_KEY", "")
    secret = os.environ.get("APP_PLAT_BLOB_SECRET_KEY", "")
    region = os.environ.get("APP_PLAT_BLOB_REGION", "").strip() or "us-east-1"
    if not endpoint or not bucket or not access or not secret:
        return "error: blob is not configured"
    if "://" not in endpoint:
        endpoint = "http://" + endpoint
    client = boto3.client(
        "s3",
        endpoint_url=endpoint,
        aws_access_key_id=access,
        aws_secret_access_key=secret,
        region_name=region,
        config=Config(
            s3={"addressing_style": "path"},
            connect_timeout=3,
            read_timeout=3,
            retries={"max_attempts": 1},
        ),
    )
    body = b"hello from python"
    client.put_object(Bucket=bucket, Key="demo.txt", Body=body, ContentType="text/plain")
    obj = client.get_object(Bucket=bucket, Key="demo.txt")
    data = obj["Body"].read()
    return "ok demo.txt=" + data.decode("utf-8", errors="replace")


def check_kv():
    prefix = os.environ.get("APP_PLAT_KV_PREFIX", "")
    if not prefix:
        return "error: APP_PLAT_KV_PREFIX is unset"
    url = os.environ.get("APP_PLAT_KV_URL", "").strip()
    if url:
        client = redis.Redis.from_url(
            url,
            socket_timeout=3,
            socket_connect_timeout=3,
            decode_responses=True,
        )
    else:
        host = os.environ.get("APP_PLAT_KV_HOST", "")
        port = os.environ.get("APP_PLAT_KV_PORT", "")
        if not host or not port:
            return "error: kv is not configured"
        client = redis.Redis(
            host=host,
            port=int(port),
            username=os.environ.get("APP_PLAT_KV_USERNAME") or None,
            password=os.environ.get("APP_PLAT_KV_PASSWORD") or None,
            socket_timeout=3,
            socket_connect_timeout=3,
            decode_responses=True,
        )
    try:
        key = prefix + "demo"
        client.set(key, "hello from python")
        got = client.get(key)
        return f"ok {key}={got}"
    finally:
        client.close()


def check_llm():
    base = os.environ.get("APP_PLAT_LLM_BASE_URL", "").strip()
    if not base:
        return "LLM is not configured"
    key = os.environ.get("APP_PLAT_LLM_API_KEY", "")
    url = base.rstrip("/") + "/models"
    req = urllib.request.Request(url, headers={"Authorization": "Bearer " + key})
    try:
        with urllib.request.urlopen(req, timeout=3) as resp:
            return f"ok status={resp.status}"
    except urllib.error.URLError as exc:
        return "error: " + redact(str(exc))
    except Exception as exc:
        return "error: " + redact(str(exc))


def redact(msg):
    for name in (
        "APP_PLAT_POSTGRES_PASSWORD",
        "APP_PLAT_POSTGRES_URL",
        "APP_PLAT_BLOB_SECRET_KEY",
        "APP_PLAT_BLOB_ACCESS_KEY",
        "APP_PLAT_KV_PASSWORD",
        "APP_PLAT_KV_URL",
        "APP_PLAT_LLM_API_KEY",
    ):
        val = os.environ.get(name, "")
        if val:
            msg = msg.replace(val, "***")
    return msg


if __name__ == "__main__":
    main()
