package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose environment (config, ffmpeg, ffprobe, workspace dir)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, used, err := resolveConfig(configPath)
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "NG config: %v\n", err)
			return fmt.Errorf("doctor: config check failed")
		}
		if ok := runDoctorChecks(cmd.OutOrStdout(), cfg, used); !ok {
			return fmt.Errorf("doctor: one or more checks failed")
		}
		return nil
	},
}

// runDoctorChecks writes one line per check and reports overall success.
// Missing ffmpeg/ffprobe or an uncreatable workspace dir fail the run.
func runDoctorChecks(out io.Writer, cfg *config.Config, usedConfig string) bool {
	ok := true

	if usedConfig == "" {
		fmt.Fprintln(out, "ok config: built-in defaults (no config.toml found)")
	} else {
		fmt.Fprintf(out, "ok config: %s\n", usedConfig)
	}

	for _, tool := range []struct{ name, path, hint string }{
		{"ffmpeg", cfg.Video.FFmpegPath, "brew install ffmpeg"},
		{"ffprobe", cfg.Video.FFprobePath, "ships with ffmpeg"},
	} {
		if _, err := exec.LookPath(tool.path); err != nil {
			fmt.Fprintf(out, "NG %s: %q not found in PATH — master will fail (%s)\n", tool.name, tool.path, tool.hint)
			ok = false
		} else {
			fmt.Fprintf(out, "ok %s: %s\n", tool.name, tool.path)
		}
	}

	if err := os.MkdirAll(cfg.Workspace.Dir, 0o755); err != nil {
		fmt.Fprintf(out, "NG workspace dir: %v\n", err)
		ok = false
	} else {
		fmt.Fprintf(out, "ok workspace dir: %s\n", cfg.Workspace.Dir)
	}

	return ok
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
