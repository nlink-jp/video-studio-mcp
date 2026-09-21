package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/logging"
	"github.com/nlink-jp/video-studio-mcp/internal/mcpserver"
	"github.com/nlink-jp/video-studio-mcp/internal/tools"
	"github.com/nlink-jp/video-studio-mcp/internal/transport"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP stdio server",
	Long:  "Read JSON-RPC messages from stdin and serve MCP tool calls. This is the default when no subcommand is given.",
	RunE:  runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, _, err := resolveConfig(configPath)
	if err != nil {
		return err
	}

	logger, logFile, err := logging.Setup(cfg.Server.LogLevel, cfg.Server.LogFile)
	if err != nil {
		return err
	}
	if logFile != nil {
		defer logFile.Close()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tr := transport.NewStdioTransport(os.Stdin, os.Stdout)
	srv := mcpserver.New("video-studio-mcp", Version, tr, logger)
	srv.SetInstructions(tools.Instructions)
	tools.Register(srv, newToolDeps(ctx, cfg, logger))

	logger.Info("serving MCP over stdio", "version", Version)
	if err := srv.Serve(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return nil
}

// resolveConfig loads the explicit config path, or searches the default
// locations, or falls back to built-in defaults. The second return value is
// the file actually used ("" for defaults).
func resolveConfig(explicit string) (*config.Config, string, error) {
	if explicit != "" {
		cfg, err := config.Load(explicit)
		return cfg, explicit, err
	}
	candidates := []string{"config.toml"}
	if dir := configDir(); dir != "" {
		candidates = []string{filepath.Join(dir, "config.toml"), "config.toml"}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			cfg, err := config.Load(c)
			return cfg, c, err
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("stat %s: %w", c, err)
		}
	}
	return config.Default(), "", nil
}

func init() {
	rootCmd.AddCommand(serveCmd)
	rootCmd.RunE = runServe
}
