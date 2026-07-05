// Package config loads the video-studio-mcp TOML configuration.
//
// Unknown keys are rejected (typo detection via toml.MetaData.Undecoded),
// and "~" is expanded in path-valued fields. Defaults are chosen so the
// server produces a standard 1080p/30fps H.264 video out of the box.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the root configuration.
type Config struct {
	Server    ServerConfig    `toml:"server"`
	Workspace WorkspaceConfig `toml:"workspace"`
	Video     VideoConfig     `toml:"video"`
}

// ServerConfig controls logging.
type ServerConfig struct {
	LogLevel string `toml:"log_level"` // debug|info|warn|error
	LogFile  string `toml:"log_file"`  // empty = stderr only
}

// WorkspaceConfig controls where the server's default workspaces live.
type WorkspaceConfig struct {
	Dir string `toml:"workspace_dir"`
}

// VideoConfig controls the ffmpeg render pipeline.
//
// Every per-page segment is encoded to identical parameters (canvas size,
// fps, pixel format, audio codec/rate) so the final concat can join them by
// stream copy without a re-encode.
type VideoConfig struct {
	FFmpegPath      string `toml:"ffmpeg_path"`
	FFprobePath     string `toml:"ffprobe_path"`
	Width           int    `toml:"width"`
	Height          int    `toml:"height"`
	FPS             int    `toml:"fps"`
	CRF             int    `toml:"crf"`     // x264 quality (0=lossless..51=worst); ~20 is visually clean
	Preset          string `toml:"preset"`  // x264 speed/size tradeoff
	PixFmt          string `toml:"pix_fmt"` // yuv420p for broad playback compatibility
	AudioBitrate    string `toml:"audio_bitrate"`
	AudioSampleRate int    `toml:"audio_sample_rate"`
	Background      string `toml:"background"` // pad color for letter/pillar-boxing (ffmpeg color name or hex)
}

// Default returns the built-in configuration.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			LogLevel: "info",
		},
		Workspace: WorkspaceConfig{
			Dir: ExpandHome("~/.video-studio"),
		},
		Video: VideoConfig{
			FFmpegPath:      "ffmpeg",
			FFprobePath:     "ffprobe",
			Width:           1920,
			Height:          1080,
			FPS:             30,
			CRF:             20,
			Preset:          "medium",
			PixFmt:          "yuv420p",
			AudioBitrate:    "192k",
			AudioSampleRate: 44100,
			Background:      "black",
		},
	}
}

// Load reads path over the defaults. Unknown keys are an error.
func Load(path string) (*Config, error) {
	cfg := Default()
	meta, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("load %s: unknown config keys: %v", path, undecoded)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	cfg.Workspace.Dir = ExpandHome(cfg.Workspace.Dir)
	cfg.Server.LogFile = ExpandHome(cfg.Server.LogFile)
	cfg.Video.FFmpegPath = ExpandHome(cfg.Video.FFmpegPath)
	cfg.Video.FFprobePath = ExpandHome(cfg.Video.FFprobePath)
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Video.Width < 16 || c.Video.Height < 16 {
		return fmt.Errorf("video.width/height must be >= 16, got %dx%d", c.Video.Width, c.Video.Height)
	}
	// yuv420p requires even dimensions.
	if c.Video.Width%2 != 0 || c.Video.Height%2 != 0 {
		return fmt.Errorf("video.width/height must be even, got %dx%d", c.Video.Width, c.Video.Height)
	}
	if c.Video.FPS < 1 {
		return fmt.Errorf("video.fps must be >= 1, got %d", c.Video.FPS)
	}
	if c.Video.CRF < 0 || c.Video.CRF > 51 {
		return fmt.Errorf("video.crf must be within [0, 51], got %d", c.Video.CRF)
	}
	if c.Video.AudioSampleRate < 8000 {
		return fmt.Errorf("video.audio_sample_rate must be >= 8000, got %d", c.Video.AudioSampleRate)
	}
	return nil
}

// ExpandHome expands a leading "~" to the user's home directory.
func ExpandHome(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if len(p) > 1 && p[1] == '/' {
		return filepath.Join(home, p[2:])
	}
	return p
}
