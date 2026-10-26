package cli

import "strings"

type catalogEntry struct {
	Name    string
	Usage   string
	Summary string
}

// catalog is the help menu shared by the CLI and the MCP capabilities tool.
var catalog = []catalogEntry{
	{"login", "login --api-key KEY [--api-url URL]", "Save API credentials to ~/.app-plat/config.json"},
	{"me", "me", "Show the current user"},
	{"keys create", "keys create --name NAME", "Create an API key"},
	{"keys list", "keys list", "List API keys"},
	{"keys revoke", "keys revoke ID", "Revoke an API key"},
	{"apps list", "apps list", "List apps"},
	{"apps create", "apps create --name NAME [--port 8080] [--persistence postgresql,blob,keyvalue] [--file app.yaml]", "Create an app"},
	{"apps get", "apps get SLUG_OR_ID", "Get an app by slug or id"},
	{"apps upload", "apps upload SLUG_OR_ID ARCHIVE", "Upload a source archive"},
	{"apps env set", "apps env set SLUG_OR_ID KEY=VALUE [--secret]", "Set an app environment variable"},
	{"apps env list", "apps env list SLUG_OR_ID", "List app environment variables"},
	{"apps delete", "apps delete SLUG_OR_ID", "Delete an app"},
	{"llm request", "llm request SLUG_OR_ID --models a,b --cap 5", "Request LLM access for an app"},
	{"mcp", "mcp", "Start the stdio MCP server"},
}

// CatalogText returns one line per command: usage, then a one-line description.
// query is an optional case-insensitive substring filter.
func CatalogText(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	var b strings.Builder
	for _, e := range catalog {
		line := e.Usage + " - " + e.Summary
		if q != "" && !strings.Contains(strings.ToLower(line), q) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return "no matching commands\n"
	}
	return b.String()
}

func usageLine(name string) string {
	for _, e := range catalog {
		if e.Name == name {
			return "usage: " + e.Usage
		}
	}
	return "usage: app-plat --help"
}
