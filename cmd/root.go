package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var configPath string

var rootCmd = &cobra.Command{
	Use:   "video-studio-mcp",
	Short: "Presentation-video compositor MCP server",
	Long: `video-studio-mcp assembles a narrated presentation video (MP4) from a page
manifest: each page pairs a still image with its audio, and the pages are
concatenated in order via ffmpeg. It is a pure compositor — slide rendering and
audio synthesis happen upstream (e.g. voice-studio-mcp supplies the audio).

When invoked with no subcommand, behaves like ` + "`video-studio-mcp serve`" + ` and reads JSON-RPC messages from stdin.`,
	// Don't dump the usage help on RunE errors; cobra still prints "Error: ..." to stderr.
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "",
		"Path to config.toml (default: search ~/.config/video-studio-mcp/config.toml then ./config.toml)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// cobra has already printed "Error: ..." to stderr.
		_ = err
		os.Exit(1)
	}
}
