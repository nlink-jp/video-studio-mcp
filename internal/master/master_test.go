package master

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/manifest"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
	"github.com/nlink-jp/video-studio-mcp/internal/workspace"
)

// fakeRunner records ffmpeg/ffprobe invocations. ffprobe calls (identified by
// the -show_entries flag) return a canned duration; ffmpeg calls materialize
// their output file (the last argument) so subsequent steps find it.
type fakeRunner struct {
	cmds     [][]string
	dur      string // ffprobe duration to report (default "1.500")
	failAt   int    // 1-based index of the call that should fail; 0 = never
	exitCode int
	stderr   string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	f.cmds = append(f.cmds, append([]string{name}, args...))
	if f.failAt > 0 && len(f.cmds) == f.failAt {
		return nil, []byte(f.stderr), f.exitCode, errors.New("exit status")
	}
	if isProbe(args) {
		d := f.dur
		if d == "" {
			d = "1.500"
		}
		return []byte(d + "\n"), nil, 0, nil
	}
	out := args[len(args)-1]
	_ = os.WriteFile(out, []byte("fake-mp4"), 0o644)
	return nil, nil, 0, nil
}

func isProbe(args []string) bool {
	for _, a := range args {
		if a == "-show_entries" {
			return true
		}
	}
	return false
}

func newMaster(r Runner) *Master {
	cfg := config.Default()
	// Real binaries need not exist for the fake runner, but Build's LookPath
	// guard runs first; point it at something that exists.
	cfg.Video.FFmpegPath = "/bin/ls"
	cfg.Video.FFprobePath = "/bin/ls"
	return &Master{Runner: r, Cfg: cfg.Video, Caption: cfg.Caption}
}

// seed writes each page's image and audio as real files inside a fresh
// workspace and returns it.
func seed(t *testing.T, pages []manifest.Page) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.NewManager(func(string) error { return nil }, func(string, string) string { return "" }).EnsureUnder(t.TempDir(), "deck1")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		writeInside(t, ws, p.Image, "img")
		writeInside(t, ws, p.Audio, "aud")
	}
	return ws
}

func writeInside(t *testing.T, ws *workspace.Workspace, rel, content string) {
	t.Helper()
	full := ws.Path(rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var twoPages = []manifest.Page{
	{Image: "images/p01.png", Audio: "audio/p01.wav"},
	{Image: "images/p02.png", Audio: "audio/p02.wav"},
}

func TestBuild(t *testing.T) {
	ws := seed(t, twoPages)
	fr := &fakeRunner{dur: "2.000"}
	m := newMaster(fr)

	res, err := m.Build(context.Background(), ws, "deck", twoPages, Options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Pages != 2 || filepath.Base(res.MasterPath) != "deck.mp4" {
		t.Errorf("result: %+v", res)
	}
	if res.DurationSeconds < 3.99 || res.DurationSeconds > 4.01 {
		t.Errorf("duration: %v (want 4.0)", res.DurationSeconds)
	}
	if res.Width != 1920 || res.Height != 1080 || res.FPS != 30 {
		t.Errorf("canvas: %+v", res)
	}

	// Calls: 2 probes + 2 segment encodes + 1 concat = 5.
	if len(fr.cmds) != 5 {
		t.Fatalf("calls: %d\n%v", len(fr.cmds), fr.cmds)
	}
	seg := strings.Join(fr.cmds[2], " ")
	for _, want := range []string{"-loop 1", "-t 2.000", "scale=1920:1080", "pad=1920:1080", "-c:v libx264", "-pix_fmt yuv420p", "-c:a aac", "seg_001.mp4"} {
		if !strings.Contains(seg, want) {
			t.Errorf("segment args missing %q: %s", want, seg)
		}
	}
	concat := strings.Join(fr.cmds[4], " ")
	for _, want := range []string{"-f concat", "-safe 0", "-c copy", "deck.mp4"} {
		if !strings.Contains(concat, want) {
			t.Errorf("concat args missing %q: %s", want, concat)
		}
	}

	// The final MP4 landed under output/.
	if _, err := os.Stat(ws.Path(workspace.DirOutput, "deck.mp4")); err != nil {
		t.Errorf("output not written: %v", err)
	}
}

func TestBuildOutputName(t *testing.T) {
	ws := seed(t, twoPages)
	m := newMaster(&fakeRunner{})
	res, err := m.Build(context.Background(), ws, "deck", twoPages, Options{OutputName: "keynote"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(res.MasterPath) != "keynote.mp4" {
		t.Errorf("output name: %s", res.MasterPath)
	}
}

func TestBuildMissingAsset(t *testing.T) {
	ws := seed(t, twoPages[:1]) // only page 1's assets exist
	m := newMaster(&fakeRunner{})

	_, err := m.Build(context.Background(), ws, "deck", twoPages, Options{})
	var te *toolerr.Error
	if !errors.As(err, &te) || te.Code != toolerr.CodeManifestIncomplete {
		t.Fatalf("want manifest_incomplete, got %v", err)
	}
	miss := te.Details["missing"].([]map[string]any)
	if len(miss) != 1 || miss[0]["page"] != 2 {
		t.Errorf("missing details: %v", miss)
	}
}

func TestBuildSymlinkRejected(t *testing.T) {
	ws := seed(t, twoPages[:1])
	// Replace page 1's image with a symlink pointing outside the workspace.
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.png")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	img := ws.Path("images/p01.png")
	if err := os.Remove(img); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, img); err != nil {
		t.Fatal(err)
	}
	_, err := m0().Build(context.Background(), ws, "deck", twoPages[:1], Options{})
	if !errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
		t.Fatalf("want path_not_allowed for symlinked asset, got %v", err)
	}
}

func m0() *Master { return newMaster(&fakeRunner{}) }

func TestBuildFFmpegFailureSurfacesStderr(t *testing.T) {
	ws := seed(t, twoPages)
	// Fail the first segment encode (call 3: after 2 probes).
	fr := &fakeRunner{failAt: 3, exitCode: 1, stderr: "Invalid data found when processing input"}
	m := newMaster(fr)

	_, err := m.Build(context.Background(), ws, "deck", twoPages, Options{})
	var te *toolerr.Error
	if !errors.As(err, &te) || te.Code != toolerr.CodeFFmpegFailed {
		t.Fatalf("want ffmpeg_failed, got %v", err)
	}
	if te.Details["exit_code"] != 1 || !strings.Contains(te.Details["stderr_tail"].(string), "Invalid data") {
		t.Errorf("details: %v", te.Details)
	}
}

func TestBuildProbeFailure(t *testing.T) {
	ws := seed(t, twoPages)
	fr := &fakeRunner{failAt: 1, exitCode: 1, stderr: "bad audio"} // first probe fails
	m := newMaster(fr)

	_, err := m.Build(context.Background(), ws, "deck", twoPages, Options{})
	if !errors.Is(err, toolerr.New(toolerr.CodeProbeFailed, "")) {
		t.Fatalf("want probe_failed, got %v", err)
	}
}

func TestBuildFFmpegNotFound(t *testing.T) {
	ws := seed(t, twoPages)
	m := newMaster(&fakeRunner{})
	m.Cfg.FFmpegPath = "definitely-no-such-ffmpeg"

	_, err := m.Build(context.Background(), ws, "deck", twoPages, Options{})
	if !errors.Is(err, toolerr.New(toolerr.CodeFFmpegNotFound, "")) {
		t.Fatalf("want ffmpeg_not_found, got %v", err)
	}
}

func TestConcatListQuoting(t *testing.T) {
	got := concatList([]string{"/a/it's.mp4"})
	if got != "file '/a/it'\\''s.mp4'\n" {
		t.Errorf("quoting: %q", got)
	}
}

func TestSegmentArgsPadColor(t *testing.T) {
	v := config.Default().Video
	v.Background = "white"
	args := strings.Join(segmentArgs(v, "/i.png", "/a.wav", "", 1.25, 0, 0, "/o.mp4"), " ")
	if !strings.Contains(args, "pad=1920:1080:(ow-iw)/2:(oh-ih)/2:white") {
		t.Errorf("pad color not applied: %s", args)
	}
	if !strings.Contains(args, "-t 1.250") {
		t.Errorf("duration not applied: %s", args)
	}
	if !strings.Contains(args, "-vf ") || strings.Contains(args, "filter_complex") {
		t.Errorf("no-caption segment should use -vf, not filter_complex: %s", args)
	}
}

func TestSegmentArgsCaptionOverlay(t *testing.T) {
	v := config.Default().Video
	args := strings.Join(segmentArgs(v, "/i.png", "/a.wav", "/c.png", 1.0, 0, 0, "/o.mp4"), " ")
	for _, want := range []string{"-i /c.png", "-filter_complex", "overlay=0:0", "-map [v]", "-map 1:a"} {
		if !strings.Contains(args, want) {
			t.Errorf("caption segment missing %q: %s", want, args)
		}
	}
}

func TestFadeDurations(t *testing.T) {
	const fps = 30
	tests := []struct {
		name            string
		cfgSec, durSec  float64
		in, out         bool
		wantIn, wantOut float64
	}{
		{name: "no boundary fades", cfgSec: 0.5, durSec: 4, wantIn: 0, wantOut: 0},
		{name: "fade in only", cfgSec: 0.5, durSec: 4, in: true, wantIn: 0.5},
		{name: "fade out only", cfgSec: 0.5, durSec: 4, out: true, wantOut: 0.5},
		{name: "both fades fit", cfgSec: 0.5, durSec: 4, in: true, out: true, wantIn: 0.5, wantOut: 0.5},
		// Half the page must stay at full brightness: one fade gets dur/2 at most.
		{name: "single fade clamped", cfgSec: 0.5, durSec: 0.6, out: true, wantOut: 0.3},
		// ...and with two fades, dur/4 each.
		{name: "both fades clamped", cfgSec: 0.5, durSec: 0.8, in: true, out: true, wantIn: 0.2, wantOut: 0.2},
		// A sub-frame fade is not a transition (1/30 s ≈ 0.033).
		{name: "sub-frame fade dropped", cfgSec: 0.5, durSec: 0.05, in: true, out: true},
		{name: "fade disabled by config", cfgSec: 0, durSec: 4, in: true, out: true},
		{name: "zero duration page", cfgSec: 0.5, durSec: 0, in: true, out: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotIn, gotOut := fadeDurations(tc.cfgSec, tc.durSec, fps, tc.in, tc.out)
			if gotIn != tc.wantIn || gotOut != tc.wantOut {
				t.Errorf("fadeDurations(%g, %g, %d, %v, %v) = (%g, %g), want (%g, %g)",
					tc.cfgSec, tc.durSec, fps, tc.in, tc.out, gotIn, gotOut, tc.wantIn, tc.wantOut)
			}
		})
	}
}

func TestSegmentArgsFade(t *testing.T) {
	v := config.Default().Video
	v.Background = "white"

	// A fade-out starts at durSec-fadeOut and dips to the canvas background.
	args := strings.Join(segmentArgs(v, "/i.png", "/a.wav", "", 4.0, 0.5, 0.5, "/o.mp4"), " ")
	for _, want := range []string{
		"fade=t=in:st=0:d=0.500:c=white",
		"fade=t=out:st=3.500:d=0.500:c=white",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("segment args missing %q: %s", want, args)
		}
	}
	// Still a plain -vf chain appended to scale/pad — no filter_complex needed.
	if !strings.Contains(args, "-vf scale=") || strings.Contains(args, "filter_complex") {
		t.Errorf("faded no-caption segment should stay on -vf: %s", args)
	}

	// Without fades, no fade filter appears at all.
	plain := strings.Join(segmentArgs(v, "/i.png", "/a.wav", "", 4.0, 0, 0, "/o.mp4"), " ")
	if strings.Contains(plain, "fade=") {
		t.Errorf("unfaded segment must carry no fade filter: %s", plain)
	}
}

func TestSegmentArgsFadeFollowsCaptionOverlay(t *testing.T) {
	// A burned-in caption must fade with its page, so the fade has to come after
	// the overlay in the filter graph — not before it.
	v := config.Default().Video
	args := strings.Join(segmentArgs(v, "/i.png", "/a.wav", "/c.png", 2.0, 0.25, 0, "/o.mp4"), " ")
	overlay := strings.Index(args, "overlay=0:0")
	fade := strings.Index(args, "fade=t=in")
	if overlay < 0 || fade < 0 {
		t.Fatalf("expected both overlay and fade filters: %s", args)
	}
	if fade < overlay {
		t.Errorf("fade must follow the caption overlay: %s", args)
	}
	if !strings.Contains(args, "format=auto,fade=t=in:st=0:d=0.250") {
		t.Errorf("fade should chain onto the overlay output: %s", args)
	}
}

func TestBuildCaptions(t *testing.T) {
	pages := []manifest.Page{
		{Image: "images/p01.png", Audio: "audio/p01.wav", Caption: "字幕テスト"},
		{Image: "images/p02.png", Audio: "audio/p02.wav"}, // no caption
	}
	ws := seed(t, pages)
	fr := &fakeRunner{dur: "1.000"}
	m := newMaster(fr)

	res, err := m.Build(context.Background(), ws, "deck", pages, Options{Captions: true, KeepIntermediate: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.CaptionsBurned != 1 {
		t.Errorf("captions_burned: %d (want 1)", res.CaptionsBurned)
	}
	if _, err := os.Stat(ws.Path(workspace.DirOutput, "tmp", "cap_001.png")); err != nil {
		t.Errorf("caption png for page 1 missing: %v", err)
	}
	if _, err := os.Stat(ws.Path(workspace.DirOutput, "tmp", "cap_002.png")); !os.IsNotExist(err) {
		t.Errorf("page 2 (no caption) should have no png, err=%v", err)
	}
	var seg1 string
	for _, c := range fr.cmds {
		j := strings.Join(c, " ")
		if strings.Contains(j, "seg_001.mp4") {
			seg1 = j
		}
	}
	if !strings.Contains(seg1, "cap_001.png") || !strings.Contains(seg1, "overlay=0:0") {
		t.Errorf("page 1 segment did not overlay its caption: %s", seg1)
	}
}

func TestBuildChapters(t *testing.T) {
	ws := seed(t, twoPages)
	fr := &fakeRunner{dur: "2.000"}
	m := newMaster(fr)

	res, err := m.Build(context.Background(), ws, "deck", twoPages, Options{Chapters: true, KeepIntermediate: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Chapters != 2 {
		t.Errorf("chapters: %d", res.Chapters)
	}
	// The concat call must map streams from input 0 and metadata from input 1.
	concat := strings.Join(fr.cmds[len(fr.cmds)-1], " ")
	if !strings.Contains(concat, "-map 0") || !strings.Contains(concat, "-map_metadata 1") {
		t.Errorf("concat missing chapter mapping: %s", concat)
	}
	// Chapter boundaries: page 1 = [0,2000), page 2 = [2000,4000).
	meta, err := os.ReadFile(ws.Path(workspace.DirOutput, "tmp", "ffmetadata.txt"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(meta)
	for _, want := range []string{";FFMETADATA1", "TIMEBASE=1/1000", "START=0", "END=2000", "START=2000", "END=4000", "title=Page 1", "title=Page 2"} {
		if !strings.Contains(s, want) {
			t.Errorf("ffmetadata missing %q:\n%s", want, s)
		}
	}
}

func TestBuildChapterTitles(t *testing.T) {
	pages := []manifest.Page{
		{Image: "images/p01.png", Audio: "audio/p01.wav", Title: "導入"},
		{Image: "images/p02.png", Audio: "audio/p02.wav"}, // falls back to "Page 2"
	}
	ws := seed(t, pages)
	m := newMaster(&fakeRunner{dur: "1.000"})

	if _, err := m.Build(context.Background(), ws, "deck", pages, Options{Chapters: true, KeepIntermediate: true}); err != nil {
		t.Fatal(err)
	}
	meta, _ := os.ReadFile(ws.Path(workspace.DirOutput, "tmp", "ffmetadata.txt"))
	s := string(meta)
	if !strings.Contains(s, "title=導入") || !strings.Contains(s, "title=Page 2") {
		t.Errorf("titles: %s", s)
	}
}

func TestBuildNoChapters(t *testing.T) {
	ws := seed(t, twoPages)
	fr := &fakeRunner{}
	m := newMaster(fr)

	res, err := m.Build(context.Background(), ws, "deck", twoPages, Options{Chapters: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.Chapters != 0 {
		t.Errorf("chapters: %d", res.Chapters)
	}
	concat := strings.Join(fr.cmds[len(fr.cmds)-1], " ")
	if strings.Contains(concat, "map_metadata") {
		t.Errorf("no-chapters run must not map metadata: %s", concat)
	}
	if _, err := os.Stat(ws.Path(workspace.DirOutput, "tmp", "ffmetadata.txt")); !os.IsNotExist(err) {
		t.Errorf("ffmetadata.txt should not exist, err=%v", err)
	}
}

func TestFFMetadataEscaping(t *testing.T) {
	s := ffmetadata([]Chapter{{Title: "a=b;c#d", StartMS: 0, EndMS: 10}})
	if !strings.Contains(s, `title=a\=b\;c\#d`) {
		t.Errorf("escaping: %s", s)
	}
}

func TestBuildCleansIntermediates(t *testing.T) {
	ws := seed(t, twoPages)
	m := newMaster(&fakeRunner{})
	if _, err := m.Build(context.Background(), ws, "deck", twoPages, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.Path(workspace.DirOutput, "tmp")); !os.IsNotExist(err) {
		t.Errorf("output/tmp should be removed after a successful render, err=%v", err)
	}
	// The master itself must remain.
	if _, err := os.Stat(ws.Path(workspace.DirOutput, "deck.mp4")); err != nil {
		t.Errorf("master missing: %v", err)
	}
}

func TestBuildKeepIntermediates(t *testing.T) {
	ws := seed(t, twoPages)
	m := newMaster(&fakeRunner{})
	if _, err := m.Build(context.Background(), ws, "deck", twoPages, Options{KeepIntermediate: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.Path(workspace.DirOutput, "tmp")); err != nil {
		t.Errorf("output/tmp should be kept, err=%v", err)
	}
}

func TestBuildCanvasOverride(t *testing.T) {
	ws := seed(t, twoPages)
	fr := &fakeRunner{dur: "1.000"}
	m := newMaster(fr)
	m.Cfg.Width, m.Cfg.Height, m.Cfg.FPS = 1080, 1920, 24 // 9:16 vertical

	res, err := m.Build(context.Background(), ws, "deck", twoPages, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 1080 || res.Height != 1920 || res.FPS != 24 {
		t.Errorf("result canvas: %+v", res)
	}
	seg := strings.Join(fr.cmds[2], " ")
	if !strings.Contains(seg, "scale=1080:1920") || !strings.Contains(seg, "pad=1080:1920") {
		t.Errorf("segment did not use overridden canvas: %s", seg)
	}
}

func TestSRTTime(t *testing.T) {
	cases := map[int]string{
		0:       "00:00:00,000",
		1500:    "00:00:01,500",
		61001:   "00:01:01,001",
		3723456: "01:02:03,456",
	}
	for ms, want := range cases {
		if got := srtTime(ms); got != want {
			t.Errorf("srtTime(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestBuildSRT(t *testing.T) {
	items := []resolved{
		{caption: "最初のページ", durSec: 2.0},
		{caption: "", durSec: 1.0}, // no caption: advances clock, no cue
		{caption: "三枚目", durSec: 1.5},
	}
	srt, cues := buildSRT(items)
	if cues != 2 {
		t.Fatalf("cues: %d (want 2)", cues)
	}
	// Cue 1 spans [0, 2.0); cue 2 (page 3) spans [3.0, 4.5).
	for _, want := range []string{
		"1\n00:00:00,000 --> 00:00:02,000\n最初のページ",
		"2\n00:00:03,000 --> 00:00:04,500\n三枚目",
	} {
		if !strings.Contains(srt, want) {
			t.Errorf("srt missing cue:\n%s\n--- got ---\n%s", want, srt)
		}
	}
}

func TestConcatArgsSubtitleAndChapters(t *testing.T) {
	// Subtitle only: input 1 is the srt, mapped and transcoded to mov_text.
	sub := strings.Join(concatArgs("/list.txt", "", "/c.srt", "/o.mp4"), " ")
	for _, want := range []string{"-i /c.srt", "-map 0", "-map 1", "-c copy", "-c:s mov_text"} {
		if !strings.Contains(sub, want) {
			t.Errorf("subtitle concat missing %q: %s", want, sub)
		}
	}
	if strings.Contains(sub, "map_metadata") {
		t.Errorf("no chapters must not map metadata: %s", sub)
	}
	// Subtitle + chapters: srt is input 1, metadata is input 2.
	both := strings.Join(concatArgs("/list.txt", "/m.txt", "/c.srt", "/o.mp4"), " ")
	for _, want := range []string{"-i /c.srt", "-i /m.txt", "-map 1", "-map_metadata 2", "-c:s mov_text"} {
		if !strings.Contains(both, want) {
			t.Errorf("subtitle+chapters concat missing %q: %s", want, both)
		}
	}
}

func TestBuildSoftCaptions(t *testing.T) {
	pages := []manifest.Page{
		{Image: "images/p01.png", Audio: "audio/p01.wav", Caption: "字幕1"},
		{Image: "images/p02.png", Audio: "audio/p02.wav"}, // no caption
	}
	ws := seed(t, pages)
	fr := &fakeRunner{dur: "1.000"}
	m := newMaster(fr)

	res, err := m.Build(context.Background(), ws, "deck", pages, Options{SoftCaptions: true, KeepIntermediate: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.SoftCaptionCues != 1 {
		t.Errorf("soft_caption_cues: %d (want 1)", res.SoftCaptionCues)
	}
	if _, err := os.Stat(ws.Path(workspace.DirOutput, "tmp", "captions.srt")); err != nil {
		t.Errorf("captions.srt not written: %v", err)
	}
	concat := strings.Join(fr.cmds[len(fr.cmds)-1], " ")
	if !strings.Contains(concat, "-c:s mov_text") || !strings.Contains(concat, "captions.srt") {
		t.Errorf("concat did not mux the soft-caption track: %s", concat)
	}
	// No page carries a caption → no srt, no subtitle track.
	ws2 := seed(t, twoPages)
	fr2 := &fakeRunner{}
	m2 := newMaster(fr2)
	res2, err := m2.Build(context.Background(), ws2, "deck", twoPages, Options{SoftCaptions: true, KeepIntermediate: true})
	if err != nil {
		t.Fatal(err)
	}
	if res2.SoftCaptionCues != 0 {
		t.Errorf("cues without captions: %d", res2.SoftCaptionCues)
	}
	if strings.Contains(strings.Join(fr2.cmds[len(fr2.cmds)-1], " "), "mov_text") {
		t.Errorf("no captions must not add a subtitle track")
	}
}

// segFor returns the recorded ffmpeg invocation that produced seg_<n>.mp4.
func segFor(t *testing.T, fr *fakeRunner, n int) string {
	t.Helper()
	want := fmt.Sprintf("seg_%03d.mp4", n)
	for _, c := range fr.cmds {
		j := strings.Join(c, " ")
		if strings.Contains(j, want) {
			return j
		}
	}
	t.Fatalf("no ffmpeg call produced %s: %v", want, fr.cmds)
	return ""
}

func TestBuildFadeTransitions(t *testing.T) {
	// p1 fades into p2, p2 fades into p3, p3's transition has no next boundary.
	pages := []manifest.Page{
		{Image: "images/p01.png", Audio: "audio/p01.wav", Transition: manifest.TransitionFade},
		{Image: "images/p02.png", Audio: "audio/p02.wav", Transition: manifest.TransitionFade},
		{Image: "images/p03.png", Audio: "audio/p03.wav", Transition: manifest.TransitionFade},
	}
	ws := seed(t, pages)
	fr := &fakeRunner{dur: "4.000"}
	m := newMaster(fr)

	res, err := m.Build(context.Background(), ws, "deck", pages, Options{Chapters: true, KeepIntermediate: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Two boundaries, so two fades — the last page's transition is ignored.
	if res.FadesApplied != 2 {
		t.Errorf("fades_applied: %d (want 2)", res.FadesApplied)
	}
	// The fade lives inside each page's own duration, so the total is still the
	// sum of the audio durations.
	if res.DurationSeconds < 11.99 || res.DurationSeconds > 12.01 {
		t.Errorf("duration: %v (want 12.0 — fades must not shorten the timeline)", res.DurationSeconds)
	}

	seg1, seg2, seg3 := segFor(t, fr, 1), segFor(t, fr, 2), segFor(t, fr, 3)
	if strings.Contains(seg1, "fade=t=in") || !strings.Contains(seg1, "fade=t=out:st=3.500:d=0.500") {
		t.Errorf("page 1 should fade out only: %s", seg1)
	}
	if !strings.Contains(seg2, "fade=t=in:st=0:d=0.500") || !strings.Contains(seg2, "fade=t=out:st=3.500:d=0.500") {
		t.Errorf("page 2 should fade in and out: %s", seg2)
	}
	if !strings.Contains(seg3, "fade=t=in:st=0:d=0.500") || strings.Contains(seg3, "fade=t=out") {
		t.Errorf("page 3 should fade in only (no trailing fade): %s", seg3)
	}

	// Chapter boundaries stay on the audio timeline: 0-4000-8000-12000.
	meta, err := os.ReadFile(ws.Path(workspace.DirOutput, "tmp", "ffmetadata.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"START=0", "END=4000", "START=4000", "END=8000", "START=8000", "END=12000"} {
		if !strings.Contains(string(meta), want) {
			t.Errorf("fades shifted the chapter timeline, missing %q:\n%s", want, meta)
		}
	}

	// The join is still a stream copy — no cross-page filter graph.
	concat := strings.Join(fr.cmds[len(fr.cmds)-1], " ")
	if !strings.Contains(concat, "-c copy") || strings.Contains(concat, "xfade") {
		t.Errorf("concat must stay a stream copy: %s", concat)
	}
}

func TestBuildCutTransitionsCarryNoFade(t *testing.T) {
	pages := []manifest.Page{
		{Image: "images/p01.png", Audio: "audio/p01.wav", Transition: manifest.TransitionCut},
		{Image: "images/p02.png", Audio: "audio/p02.wav"}, // default = cut
	}
	ws := seed(t, pages)
	fr := &fakeRunner{dur: "2.000"}
	m := newMaster(fr)

	res, err := m.Build(context.Background(), ws, "deck", pages, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FadesApplied != 0 {
		t.Errorf("fades_applied: %d (want 0)", res.FadesApplied)
	}
	for _, c := range fr.cmds {
		if strings.Contains(strings.Join(c, " "), "fade=") {
			t.Errorf("cut-only deck must carry no fade filter: %v", c)
		}
	}
}

func TestBuildFadeSecondsZeroDisablesFading(t *testing.T) {
	pages := []manifest.Page{
		{Image: "images/p01.png", Audio: "audio/p01.wav", Transition: manifest.TransitionFade},
		{Image: "images/p02.png", Audio: "audio/p02.wav"},
	}
	ws := seed(t, pages)
	fr := &fakeRunner{dur: "2.000"}
	m := newMaster(fr)
	m.Cfg.FadeSeconds = 0

	res, err := m.Build(context.Background(), ws, "deck", pages, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FadesApplied != 0 {
		t.Errorf("fades_applied with fade_seconds=0: %d (want 0)", res.FadesApplied)
	}
	if strings.Contains(segFor(t, fr, 1), "fade=") {
		t.Errorf("fade_seconds=0 must render every boundary as a cut: %s", segFor(t, fr, 1))
	}
}

func TestBuildFadeClampedOnShortPages(t *testing.T) {
	// 0.8s pages: page 2 fades on both sides, so each fade gets dur/4 = 0.2s.
	pages := []manifest.Page{
		{Image: "images/p01.png", Audio: "audio/p01.wav", Transition: manifest.TransitionFade},
		{Image: "images/p02.png", Audio: "audio/p02.wav", Transition: manifest.TransitionFade},
		{Image: "images/p03.png", Audio: "audio/p03.wav"},
	}
	ws := seed(t, pages)
	fr := &fakeRunner{dur: "0.800"}
	m := newMaster(fr)

	if _, err := m.Build(context.Background(), ws, "deck", pages, Options{KeepIntermediate: true}); err != nil {
		t.Fatal(err)
	}
	// Page 1 has one fade → 0.8/2 = 0.4s, starting at 0.4s.
	if !strings.Contains(segFor(t, fr, 1), "fade=t=out:st=0.400:d=0.400") {
		t.Errorf("page 1 fade not clamped to half the page: %s", segFor(t, fr, 1))
	}
	// Page 2 has two → 0.2s each, the trailing one starting at 0.6s.
	seg2 := segFor(t, fr, 2)
	if !strings.Contains(seg2, "fade=t=in:st=0:d=0.200") || !strings.Contains(seg2, "fade=t=out:st=0.600:d=0.200") {
		t.Errorf("page 2 fades not clamped to a quarter each: %s", seg2)
	}
}

// TestBuildDoesNotFollowLinkedOutput pins the ADR-0010 boundary at the one
// path that leaves the containment root: the final MP4. ffmpeg cannot inherit
// os.Root — it opens the output path itself — so a symlink planted at
// output/<name>.mp4 was followed and the link's target overwritten with the
// render.
//
// Which layer this observes: the spawn boundary, end to end. fakeRunner
// materializes each command's output with os.WriteFile on the path it was
// handed, which follows a final-component symlink exactly as ffmpeg does, so
// the assertion below is on the real outcome and not merely on the
// preparation step. No real ffmpeg is needed.
func TestBuildDoesNotFollowLinkedOutput(t *testing.T) {
	// EvalSymlinks first: on macOS t.TempDir() sits under /var, itself a link.
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim.mp4")
	if err := os.WriteFile(victim, []byte("do not overwrite me"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := seed(t, twoPages)
	if err := os.Symlink(victim, ws.Path(workspace.DirOutput, "deck.mp4")); err != nil {
		t.Fatal(err)
	}

	res, err := newMaster(&fakeRunner{}).Build(context.Background(), ws, "deck", twoPages, Options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if got, err := os.ReadFile(victim); err != nil || string(got) != "do not overwrite me" {
		t.Errorf("the link's target was overwritten: %q err=%v", got, err)
	}
	// The render still had to land inside the workspace, as a real file.
	fi, err := os.Lstat(res.MasterPath)
	if err != nil {
		t.Fatalf("master not written: %v", err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("master is not a regular file (mode %s): the link survived the render", fi.Mode())
	}
}

// A page name ffmpeg would read as a pattern is refused before anything is
// handed to it: p%d.png is a regular file here, and ffmpeg would open p0.png,
// p1.png, … instead — files nobody judged.
func TestAPageNameThatIsAPatternIsRefused(t *testing.T) {
	for _, name := range []string{"p%d.png", "p*.png", "p[0].png", "p{a,b}.png", "p?.png"} {
		pages := []manifest.Page{{Image: name, Audio: "a.wav"}}
		ws := seed(t, pages)
		r := &fakeRunner{}
		_, err := newMaster(r).Build(context.Background(), ws, "deck", pages, Options{})
		if !errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
			t.Errorf("%s: err = %v, want path_not_allowed", name, err)
		}
		if len(r.cmds) != 0 {
			t.Errorf("%s: %d command(s) ran", name, len(r.cmds))
		}
	}
}

// swapRunner swaps page 2's image for a link while page 1 renders, as a
// caller writing to its own workspace could during a long async render.
type swapRunner struct {
	fakeRunner
	swap    func()
	onProbe bool // swap during the first ffprobe call rather than the first ffmpeg one
	done    bool
}

func (s *swapRunner) Run(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	if !s.done && isProbe(args) == s.onProbe {
		s.done = true
		s.swap()
	}
	return s.fakeRunner.Run(ctx, name, args)
}

// Each page is verified again immediately before ffmpeg opens it: page 2
// swapped for a link after the first check is refused, never handed over.
func TestAPageSwappedDuringTheRenderIsNotHandedToFFmpeg(t *testing.T) {
	pages := []manifest.Page{{Image: "p1.png", Audio: "a1.wav"}, {Image: "p2.png", Audio: "a2.wav"}}
	ws := seed(t, pages)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &swapRunner{swap: func() {
		p2 := ws.Path("p2.png")
		if err := os.Remove(p2); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, p2); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}}
	_, err := newMaster(r).Build(context.Background(), ws, "deck", pages, Options{})
	if !errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
		t.Fatalf("err = %v, want path_not_allowed for the swapped page", err)
	}
	for _, c := range r.cmds {
		for _, a := range c {
			if a == ws.Path("p2.png") {
				t.Fatalf("the swapped page was handed to %s", c[0])
			}
		}
	}
}

// The same before ffprobe: page 2's audio swapped for a link while page 1's is
// probed is refused, never probed.
func TestAnAudioSwappedDuringTheProbesIsNotHandedToFFprobe(t *testing.T) {
	pages := []manifest.Page{{Image: "p1.png", Audio: "a1.wav"}, {Image: "p2.png", Audio: "a2.wav"}}
	ws := seed(t, pages)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &swapRunner{onProbe: true, swap: func() {
		a2 := ws.Path("a2.wav")
		if err := os.Remove(a2); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, a2); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}}
	_, err := newMaster(r).Build(context.Background(), ws, "deck", pages, Options{})
	if !errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
		t.Fatalf("err = %v, want path_not_allowed for the swapped audio", err)
	}
	for _, c := range r.cmds {
		for _, a := range c {
			if a == ws.Path("a2.wav") {
				t.Fatalf("the swapped audio was handed to %s", c[0])
			}
		}
	}
}
