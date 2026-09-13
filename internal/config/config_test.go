package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*VideoConfig)
		wantErr bool
	}{
		{name: "defaults", mutate: func(*VideoConfig) {}},
		{name: "odd width", mutate: func(v *VideoConfig) { v.Width = 1921 }, wantErr: true},
		{name: "tiny canvas", mutate: func(v *VideoConfig) { v.Width, v.Height = 8, 8 }, wantErr: true},
		{name: "zero fps", mutate: func(v *VideoConfig) { v.FPS = 0 }, wantErr: true},
		{name: "crf out of range", mutate: func(v *VideoConfig) { v.CRF = 52 }, wantErr: true},
		{name: "low sample rate", mutate: func(v *VideoConfig) { v.AudioSampleRate = 4000 }, wantErr: true},

		// fade_seconds: 0 disables fading, so it is valid; negative is not, and a
		// value past maxFadeSeconds would be silently swallowed by the per-page
		// clamp, so it is rejected where the caller can still see it.
		{name: "fade disabled", mutate: func(v *VideoConfig) { v.FadeSeconds = 0 }},
		{name: "fade at maximum", mutate: func(v *VideoConfig) { v.FadeSeconds = maxFadeSeconds }},
		{name: "negative fade", mutate: func(v *VideoConfig) { v.FadeSeconds = -0.1 }, wantErr: true},
		{name: "fade past maximum", mutate: func(v *VideoConfig) { v.FadeSeconds = maxFadeSeconds + 0.1 }, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Default().Video
			tc.mutate(&v)
			err := v.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("Validate() = nil, want an error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestDefaultFadeSeconds(t *testing.T) {
	if got := Default().Video.FadeSeconds; got != 0.5 {
		t.Errorf("default fade_seconds = %g, want 0.5", got)
	}
}

// A key this server removed must be answered by name: the operator set it
// deliberately, and "unknown config keys" reads like a typo. v0.5.0 removed
// the default workspace root but left the key decoding into a field nothing
// used, so a config carrying it loaded and quietly meant nothing.
func TestRemovedWorkspaceDirIsRejectedByName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[workspace]\nworkspace_dir = \"~/.video-studio\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("a config carrying workspace_dir must fail to load")
	}
	for _, want := range []string{"workspace_dir", "work_dir", "ADR-0008"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
