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
	Server  ServerConfig  `toml:"server"`
	Video   VideoConfig   `toml:"video"`
	Caption CaptionConfig `toml:"caption"`
}

// ServerConfig controls logging.
type ServerConfig struct {
	LogLevel string `toml:"log_level"` // debug|info|warn|error
	LogFile  string `toml:"log_file"`  // empty = stderr only
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
	// FadeSeconds is the length of one fade at a page boundary whose manifest
	// transition is "fade" (ADR-0007). Clamped per page so at least half of
	// every page stays at full brightness; 0 disables fading (every boundary
	// becomes a cut).
	FadeSeconds float64 `toml:"fade_seconds"`
}

// CaptionConfig controls burned-in caption rendering (opt-in via master's
// captions flag). Text is rendered to a transparent overlay in Go using the
// bundled M PLUS 1p font, then composited by ffmpeg's overlay filter — no
// dependency on ffmpeg being built with libfreetype.
type CaptionConfig struct {
	FontSize   float64 `toml:"font_size"`   // px at the configured canvas height
	FontColor  string  `toml:"font_color"`  // #RRGGBB / #RRGGBBAA / white|black
	BoxColor   string  `toml:"box_color"`   // background box color behind the text
	BoxOpacity float64 `toml:"box_opacity"` // 0..1 applied to the box color's alpha
	MarginV    int     `toml:"margin_v"`    // px from the bottom edge to the box
	MarginH    int     `toml:"margin_h"`    // px kept clear on each side (wrap width)
	BoxPadH    int     `toml:"box_pad_h"`   // horizontal padding inside the box
	BoxPadV    int     `toml:"box_pad_v"`   // vertical padding inside the box
}

// Default returns the built-in configuration.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			LogLevel: "info",
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
			FadeSeconds:     0.5,
		},
		Caption: CaptionConfig{
			FontSize:   48,
			FontColor:  "#FFFFFF",
			BoxColor:   "#000000",
			BoxOpacity: 0.5,
			MarginV:    60,
			MarginH:    80,
			BoxPadH:    20,
			BoxPadV:    12,
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
		// A key this server removed deserves its name and its reason, not the
		// same "unknown key" a typo gets: the operator set it deliberately and
		// has to be told what replaced it (ADR-0008).
		for _, k := range undecoded {
			if k.String() == "workspace.workspace_dir" {
				return nil, fmt.Errorf("load %s: [workspace] workspace_dir was removed in ADR-0008: "+
					"the workspace is <work_dir>/<workspace_id>/ and work_dir is named by the caller on "+
					"every call, so the server owns no root. Delete the key and the [workspace] section", path)
			}
		}
		return nil, fmt.Errorf("load %s: unknown config keys: %v", path, undecoded)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	cfg.Server.LogFile = ExpandHome(cfg.Server.LogFile)
	cfg.Video.FFmpegPath = ExpandHome(cfg.Video.FFmpegPath)
	cfg.Video.FFprobePath = ExpandHome(cfg.Video.FFprobePath)
	return cfg, nil
}

// Validate checks the video parameters. It is exported so the master tool can
// re-validate a config whose canvas has been overridden per call.
func (v VideoConfig) Validate() error {
	if v.Width < 16 || v.Height < 16 {
		return fmt.Errorf("width/height must be >= 16, got %dx%d", v.Width, v.Height)
	}
	// yuv420p requires even dimensions.
	if v.Width%2 != 0 || v.Height%2 != 0 {
		return fmt.Errorf("width/height must be even, got %dx%d", v.Width, v.Height)
	}
	if v.FPS < 1 {
		return fmt.Errorf("fps must be >= 1, got %d", v.FPS)
	}
	if v.CRF < 0 || v.CRF > 51 {
		return fmt.Errorf("crf must be within [0, 51], got %d", v.CRF)
	}
	if v.AudioSampleRate < 8000 {
		return fmt.Errorf("audio_sample_rate must be >= 8000, got %d", v.AudioSampleRate)
	}
	// A fade longer than maxFadeSeconds is a fade for a page that does not
	// exist; the per-page clamp would swallow it silently, so reject it here
	// where the caller can see the mistake.
	if v.FadeSeconds < 0 || v.FadeSeconds > maxFadeSeconds {
		return fmt.Errorf("fade_seconds must be within [0, %g], got %g", maxFadeSeconds, v.FadeSeconds)
	}
	return nil
}

// maxFadeSeconds bounds fade_seconds. Half of it is the longest fade any page
// can actually receive (the per-page clamp keeps half of each page at full
// brightness), which is already far past anything a presentation deck wants.
const maxFadeSeconds float64 = 10

func (c *Config) validate() error {
	if err := c.Video.Validate(); err != nil {
		return fmt.Errorf("video.%w", err)
	}
	if c.Caption.FontSize <= 0 {
		return fmt.Errorf("caption.font_size must be > 0, got %g", c.Caption.FontSize)
	}
	if c.Caption.BoxOpacity < 0 || c.Caption.BoxOpacity > 1 {
		return fmt.Errorf("caption.box_opacity must be within [0, 1], got %g", c.Caption.BoxOpacity)
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
