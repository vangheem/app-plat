package cp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/app-plat/app-plat/internal/store"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

func (s *Server) buildApp(ctx context.Context, app *store.App) error {
	if app.SourcePath == "" {
		return fmt.Errorf("no source archive")
	}
	dir, err := os.MkdirTemp("", "app-plat-"+app.Slug+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := extractArchive(app.SourcePath, dir); err != nil {
		return err
	}
	root := collapseRoot(dir)
	kind, err := ensureDockerfile(root, app.Port)
	if err != nil {
		return err
	}
	image := fmt.Sprintf("app-plat/%s:%d", app.Slug, time.Now().Unix())
	cmd := exec.CommandContext(ctx, "docker", "build", "-f", dockerfilePath(root), "-t", image, root)
	out, err := cmd.CombinedOutput()
	app.BuildLog = string(out)
	app.DetectedType = kind
	if err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	app.Image = image
	if perr := s.pushImage(ctx, image, app.Slug); perr != nil {
		app.BuildLog += "\nregistry push skipped: " + perr.Error()
		s.log.Warn("registry push", "app", app.Slug, "err", perr)
	}
	return nil
}

func dockerfilePath(root string) string {
	if _, err := os.Stat(filepath.Join(root, "Dockerfile")); err == nil {
		return filepath.Join(root, "Dockerfile")
	}
	if _, err := os.Stat(filepath.Join(root, "dockerfile")); err == nil {
		return filepath.Join(root, "dockerfile")
	}
	return filepath.Join(root, "Dockerfile.app-plat")
}

func ensureDockerfile(root string, port int) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "Dockerfile")); err == nil {
		return "dockerfile", nil
	}
	if _, err := os.Stat(filepath.Join(root, "dockerfile")); err == nil {
		return "dockerfile", nil
	}
	kind, body, err := generateDockerfile(root, port)
	if err != nil {
		return "", err
	}
	return kind, os.WriteFile(filepath.Join(root, "Dockerfile.app-plat"), []byte(body), 0o644)
}

func generateDockerfile(root string, port int) (string, string, error) {
	switch {
	case exists(root, "Cargo.toml"):
		bin := cargoBin(root)
		return "rust", fmt.Sprintf(`FROM rust:1.85-alpine AS build
RUN apk add --no-cache musl-dev
WORKDIR /src
COPY . .
RUN cargo build --release
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /src/target/release/%s /app
ENV PORT=%d
EXPOSE %d
CMD ["/app"]
`, bin, port, port), nil
	case exists(root, "go.mod"):
		return "golang", fmt.Sprintf(`FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app .
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/app /app
ENV PORT=%d
EXPOSE %d
CMD ["/app"]
`, port, port), nil
	case exists(root, "package.json"):
		return "node", fmt.Sprintf(`FROM node:22-slim
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install
COPY . .
ENV PORT=%d
EXPOSE %d
CMD ["npm", "start"]
`, port, port), nil
	case exists(root, "uv.lock") || exists(root, "pyproject.toml"):
		entry := pyEntry(root)
		return "python-uv", fmt.Sprintf(`FROM python:3.12-slim
COPY --from=ghcr.io/astral-sh/uv:0.5.11 /uv /usr/local/bin/uv
WORKDIR /app
COPY . .
RUN uv sync --frozen --no-dev || uv sync --no-dev || uv pip install --system -r requirements.txt
ENV PORT=%d
EXPOSE %d
CMD ["uv", "run", "python", "%s"]
`, port, port, entry), nil
	case exists(root, "requirements.txt"):
		entry := pyEntry(root)
		return "python-pip", fmt.Sprintf(`FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
ENV PORT=%d
EXPOSE %d
CMD ["python", "%s"]
`, port, port, entry), nil
	default:
		return "", "", fmt.Errorf("no Dockerfile and could not detect python, node, golang, or rust; add a Dockerfile")
	}
}

func pyEntry(root string) string {
	for _, name := range []string{"app.py", "main.py", "server.py"} {
		if exists(root, name) {
			return name
		}
	}
	return "app.py"
}

func cargoBin(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
	if err != nil {
		return "app"
	}
	inPackage := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inPackage = line == "[package]"
			continue
		}
		if inPackage && strings.HasPrefix(line, "name") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			}
		}
	}
	return "app"
}

func exists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

func (s *Server) pushImage(ctx context.Context, localImage, slug string) error {
	f, err := os.CreateTemp("", "app-plat-image-*.tar")
	if err != nil {
		return err
	}
	f.Close()
	defer os.Remove(f.Name())
	cmd := exec.CommandContext(ctx, "docker", "save", "-o", f.Name(), localImage)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker save: %w (%s)", err, tail(string(out), 300))
	}
	ref := strings.TrimPrefix(localImage, "app-plat/")
	tag, err := name.NewTag(s.cfg.Registry+"/app-plat/"+ref, name.Insecure)
	if err != nil {
		return err
	}
	img, err := tarball.ImageFromPath(f.Name(), nil)
	if err != nil {
		return err
	}
	return remote.Write(tag, img, remote.WithContext(ctx))
}

func extractArchive(path, dest string) error {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".zip") {
		return extractZip(path, dest)
	}
	return extractTar(path, dest)
}

func extractTar(path, dest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") || strings.HasSuffix(strings.ToLower(path), ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}

func extractZip(path, dest string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func safeJoin(root, name string) (string, error) {
	clean := filepath.Clean("/" + name)
	target := filepath.Join(root, clean)
	if !strings.HasPrefix(target, filepath.Clean(root)+string(os.PathSeparator)) && target != filepath.Clean(root) {
		return "", fmt.Errorf("illegal path %s", name)
	}
	return target, nil
}

func collapseRoot(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return dir
	}
	return filepath.Join(dir, entries[0].Name())
}

func readAppYAML(archive string) (string, error) {
	dir, err := os.MkdirTemp("", "app-yaml-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if err := extractArchive(archive, dir); err != nil {
		return "", err
	}
	root := collapseRoot(dir)
	raw, err := os.ReadFile(filepath.Join(root, "app.yaml"))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
