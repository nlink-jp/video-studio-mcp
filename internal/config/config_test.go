package config

import "testing"

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
