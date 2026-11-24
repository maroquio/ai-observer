package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tobilg/ai-observer/internal/tools"
)

// setting is an environment variable AI Observer reads
type setting struct {
	name, help string
}

// settingGroup is a heading of the help's settings and its settings
type settingGroup struct {
	title    string
	settings []setting
}

// settings are all environment variables AI Observer reads, in the order
// the help lists them (a test checks the list against the code). Command
// flags that set the same override them.
var settings = []settingGroup{
	{"Servers", []setting{
		{"AI_OBSERVER_HOST", "Interface the servers listen on: 127.0.0.1 (default) or 0.0.0.0 for all (--host)"},
		{"AI_OBSERVER_API_PORT", "API and dashboard port (default: 8080; --api-port)"},
		{"AI_OBSERVER_OTLP_PORT", "OTLP/HTTP ingestion port (default: 4318; --otlp-port)"},
		{"AI_OBSERVER_OTLP_GRPC_PORT", "OTLP/gRPC ingestion port, 0 or off to turn it off (default: 4317; --otlp-grpc-port)"},
		{"AI_OBSERVER_FRONTEND_URL", "Origin of a dashboard served elsewhere, allowed by CORS (default: http://localhost:5173)"},
		{"AI_OBSERVER_ALLOWED_HOSTS", "Further host names the API accepts, comma-separated; * accepts any"},
		{"AI_OBSERVER_MCP", "Serve the MCP endpoint /mcp for agents on the API port: true or false (default: false; --mcp)"},
		{"AI_OBSERVER_ADMIN_API", "Serve the maintenance endpoints /api/admin/*: true or false (default: false)"},
	}},
	{"Security", []setting{
		{"AI_OBSERVER_AUTH_TOKEN", "Token required on OTLP and API requests (default: none)"},
		{"AI_OBSERVER_TLS_CERT", "PEM certificate file: HTTPS and gRPC over TLS, with AI_OBSERVER_TLS_KEY (default: none)"},
		{"AI_OBSERVER_TLS_KEY", "PEM private key file of AI_OBSERVER_TLS_CERT"},
		{"AI_OBSERVER_REDACT", "Redact telemetry before storing it: secrets (default), all (secrets and personal data) or off"},
	}},
	{"Database", []setting{
		{"AI_OBSERVER_DATABASE_PATH", "DuckDB database file (default: ./data/ai-observer.duckdb; --db)"},
		{"AI_OBSERVER_DB_MEMORY_LIMIT", "Memory DuckDB may use, such as 2GB (default: 80% of the memory)"},
		{"AI_OBSERVER_DB_THREADS", "Threads DuckDB runs queries on (default: one per CPU core)"},
		{"AI_OBSERVER_RETENTION_DAYS", "Delete telemetry older than this many days (default: keep all)"},
		{"AI_OBSERVER_MAX_DB_SIZE", "Delete the oldest days while the stored data exceeds this size, such as 20GB (default: no limit)"},
		{"AI_OBSERVER_STORE_ALL_SPANS", "Also store the tools' noise spans: true or false (default: false)"},
		{"AI_OBSERVER_BACKUP_DIR", "Directory the admin API writes backups and exports to (default: backups next to the database)"},
		{"AI_OBSERVER_SKIP_MIGRATION_BACKUP", "Migrate the database without copying it first: true or false (default: false)"},
	}},
	{"HTTP capture proxy (Anthropic API traffic of Claude Code)", []setting{
		{"AI_OBSERVER_HTTP_PROXY_ENABLED", "Run the capture proxy with serve and watch: true or false (default: false; --http-proxy)"},
		{"AI_OBSERVER_HTTP_PROXY_HOST", "Interface the proxy listens on (default: 127.0.0.1; others need AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE)"},
		{"AI_OBSERVER_HTTP_PROXY_PORT", "Shared proxy port for ANTHROPIC_BASE_URL, or off for launcher runs only (default: 8787; --http-proxy-port)"},
		{"AI_OBSERVER_HTTP_PROXY_UPSTREAM", "Fixed destination: https, or http to loopback (default: https://api.anthropic.com)"},
		{"AI_OBSERVER_HTTP_CAPTURE_MODE", "safe (redacted bodies, default), metadata_only (no bodies) or full_raw (also unredacted bodies)"},
		{"AI_OBSERVER_HTTP_CAPTURE_DIR", "Directory of the stored bodies (default: http-captures next to the database)"},
		{"AI_OBSERVER_HTTP_CAPTURE_MAX_BODY_BYTES", "Largest body stored, such as 32MB; larger ones are forwarded whole, stored cut (default: 32MB)"},
		{"AI_OBSERVER_HTTP_CAPTURE_QUEUE_SIZE", "Capture records waiting to be stored; more are dropped and counted, never delaying traffic (default: 1024)"},
		{"AI_OBSERVER_HTTP_CAPTURE_RETENTION_DAYS", "Delete capture runs older than this many days, 0 keeps them (default: AI_OBSERVER_RETENTION_DAYS)"},
		{"AI_OBSERVER_HTTP_CAPTURE_STORAGE_LIMIT", "Delete the oldest finished runs while the stored bodies exceed this size, or off (default: 2GB)"},
		{"AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE", "Let the proxy listen beyond loopback: true or false (default: false)"},
	}},
	{"Logs", []setting{
		{"AI_OBSERVER_LOG_LEVEL", "DEBUG, INFO (default), WARN or ERROR"},
		{"AI_OBSERVER_LOG_FORMAT", "text (default) or json"},
	}},
	{"Session files and prices", []setting{
		{"AI_OBSERVER_CLAUDE_PATH", "Claude Code directories to read, comma-separated (default: ~/.config/claude/projects and ~/.claude/projects)"},
		{"AI_OBSERVER_CODEX_PATH", "Codex directory to read (default: $CODEX_HOME/sessions or ~/.codex/sessions)"},
		{"AI_OBSERVER_GEMINI_PATH", "Gemini CLI directory to read (default: $GEMINI_HOME/tmp or ~/.gemini/tmp)"},
		{"AI_OBSERVER_PRICING_DIR", "Directory with price files over the built-in prices (written by ai-observer pricing update)"},
	}},
}

// printSettings prints the settings, grouped, their names aligned
func printSettings(w io.Writer) {
	width := 0
	for _, g := range settings {
		for _, s := range g.settings {
			width = max(width, len(s.name))
		}
	}
	for i, g := range settings {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "  %s:\n", g.title)
		for _, s := range g.settings {
			fmt.Fprintf(w, "    %-*s  %s\n", width, s.name, s.help)
		}
	}
}

// argument is a positional argument of a command and what it selects
type argument struct {
	name, help string
}

// printArguments prints a command's arguments, their names aligned
func printArguments(w io.Writer, args []argument) {
	width := 0
	for _, a := range args {
		width = max(width, len(a.name))
	}
	fmt.Fprintln(w, "Arguments:")
	for _, a := range args {
		fmt.Fprintf(w, "  %-*s  %s\n", width, a.name, a.help)
	}
	fmt.Fprintln(w)
}

// toolArguments are the arguments of commands that take a tool, the tools
// with session files (fileTools) or all, then "all" described by all
func toolArguments(fileTools bool, describe func(p tools.Provider) string, all string) []argument {
	var args []argument
	for _, p := range tools.Providers() {
		if fileTools && !p.SessionFiles {
			continue
		}
		args = append(args, argument{string(p.Tool), describe(p)})
	}
	return append(args, argument{"all", all})
}

// toolList is the names of the tools, for a usage line: claude-code|codex|...
func toolList(fileTools bool) string {
	var names []string
	for _, p := range tools.Providers() {
		if !fileTools || p.SessionFiles {
			names = append(names, string(p.Tool))
		}
	}
	return strings.Join(names, "|")
}

// valueFlag is a string flag whose help names its value (TypeName), such as
// "--from date"
type valueFlag struct {
	value    *string
	typeName string
}

func (f valueFlag) String() string {
	if f.value == nil {
		return ""
	}
	return *f.value
}

func (f valueFlag) Set(s string) error {
	*f.value = s
	return nil
}

func (f valueFlag) TypeName() string { return f.typeName }

// registerDateFlags defines --from and --to, the first and last local day
// of what a command selects (importer.ParseDateArg), described by what such
// as "import"; required marks them as required
func registerDateFlags(fs *flag.FlagSet, from, to *string, what string, required bool) {
	suffix := ")"
	if required {
		suffix = ", required)"
	}
	fs.Var(valueFlag{from, "date"}, "from", "First day to "+what+" (YYYY-MM-DD, local time"+suffix)
	fs.Var(valueFlag{to, "date"}, "to", "Last day to "+what+" (YYYY-MM-DD, local time"+suffix)
}

// registerYesFlag defines --yes, which answers the confirmation prompt
func registerYesFlag(fs *flag.FlagSet, yes *bool) {
	fs.BoolVar(yes, "yes", false, "Skip the confirmation prompt")
}

// registerDryRunFlag defines --dry-run, which shows what a command would do
// (shows) without changing anything
func registerDryRunFlag(fs *flag.FlagSet, dryRun *bool, shows string) {
	fs.BoolVar(dryRun, "dry-run", false, "Show "+shows+", without changing anything")
}

// exitOnError ends a command that failed: it prints err and exits with
// status 1. A --help request (flag.ErrHelp) is no failure: the usage has
// been printed.
func exitOnError(err error) {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return
	}
	fmt.Printf("Error: %v\n", err)
	os.Exit(1)
}

// Help texts of options several commands share
const (
	mcpHelp      = "Serve the MCP endpoint /mcp for agents on the API port (AI_OBSERVER_MCP)"
	backfillHelp = "Import the existing files on the first start"
)
