package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/pavelpilyak/devrecall/internal/agent/tools"
	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/embedding"
	"github.com/pavelpilyak/devrecall/internal/mcp"
	"github.com/pavelpilyak/devrecall/internal/storage"
)

// newMCPCmd starts an MCP stdio server. Intended to be wired into a coding
// agent (Claude Code, Cursor, Codex, Continue, Zed) via its MCP config —
// the agent spawns `devrecall mcp` and talks JSON-RPC over the pipe.
//
// Logs to stderr only; stdout is reserved for protocol messages.
func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP stdio server (for Claude Code, Cursor, etc.)",
		Long: `Starts a Model Context Protocol stdio server that exposes DevRecall's
tool catalogue to MCP-compatible coding agents.

The server reads from stdin and writes to stdout — log output goes to
stderr. Local-only by design; there is no listening socket.

Configure your coding tool to launch this command. Claude Code, Cursor,
Codex, Continue and Zed all support MCP servers spawned via stdio.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			// Embedder is optional. If unavailable, semantic_search returns
			// a runtime error; the rest of the catalogue still works.
			dir, err := config.Dir()
			if err != nil {
				return fmt.Errorf("config dir: %w", err)
			}
			tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return fmt.Errorf("token store: %w", err)
			}
			embedder, err := embedding.FromConfig(cfg, tokenStore)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: embedding unavailable, semantic_search disabled: %v\n", err)
				embedder = nil
			}

			registry := tools.NewRegistry(tools.Deps{
				DB:       db,
				Embedder: embedder,
			})

			server := mcp.NewServer(registry, version, os.Stdin, os.Stdout)
			return server.Serve(cmd.Context())
		},
	}
}
