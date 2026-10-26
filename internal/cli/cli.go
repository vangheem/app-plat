// Package cli implements the app-plat command line.
// Command output is JSON unless --text is set. The mcp subcommand is started
// by cmd/app-plat so this package does not speak JSON-RPC itself.
package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/app-plat/app-plat/pkg/apiclient"
)

// Main runs the CLI and returns a process exit code.
func Main(args []string) int {
	out, err := Execute(context.Background(), args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if out != "" {
		fmt.Fprintln(os.Stdout, out)
	}
	return 0
}

// Execute runs one command in-process and returns the text that would be
// written to stdout. It never starts a shell.
func Execute(ctx context.Context, args []string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	flagURL, text, rest, err := extractGlobal(args)
	if err != nil {
		return "", err
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		return CatalogText(""), nil
	}
	switch rest[0] {
	case "login":
		return cmdLogin(rest[1:], flagURL, text)
	case "mcp":
		return "", fmt.Errorf("mcp cannot be run through execute")
	}

	cfg, err := resolve(flagURL)
	if err != nil {
		return "", err
	}
	switch rest[0] {
	case "me":
		return cmdMe(ctx, cfg, rest[1:], text)
	case "keys":
		return cmdKeys(ctx, cfg, rest[1:], text)
	case "apps":
		return cmdApps(ctx, cfg, rest[1:], text)
	case "llm":
		return cmdLLM(ctx, cfg, rest[1:], text)
	default:
		return "", fmt.Errorf("unknown command %q", rest[0])
	}
}

func newClient(cfg fileConfig) *apiclient.Client {
	c := apiclient.New(cfg.APIURL)
	c.Token = cfg.APIKey
	if c.HTTP == nil {
		c.HTTP = &http.Client{}
	}
	c.HTTP.Timeout = 2 * time.Minute
	return c
}

func authed(cfg fileConfig) (*apiclient.Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("not logged in: run app-plat login --api-key apk_... or set APP_PLAT_API_KEY")
	}
	if strings.TrimSpace(cfg.APIURL) == "" {
		return nil, fmt.Errorf("api url is not configured")
	}
	return newClient(cfg), nil
}
