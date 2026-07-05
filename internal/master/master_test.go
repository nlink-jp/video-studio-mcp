package master

import (
	"context"
	"errors"
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
	ws, err := workspace.NewManager(t.TempDir()).Ensure("deck1")
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
	args := strings.Join(segmentArgs(v, "/i.png", "/a.wav", "", 1.25, "/o.mp4"), " ")
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
	args := strings.Join(segmentArgs(v, "/i.png", "/a.wav", "/c.png", 1.0, "/o.mp4"), " ")
	for _, want := range []string{"-i /c.png", "-filter_complex", "overlay=0:0", "-map [v]", "-map 1:a"} {
		if !strings.Contains(args, want) {
			t.Errorf("caption segment missing %q: %s", want, args)
		}
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
