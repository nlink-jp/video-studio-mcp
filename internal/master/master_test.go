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
	cfg := config.Default().Video
	// Real binaries need not exist for the fake runner, but Build's LookPath
	// guard runs first; point it at something that exists.
	cfg.FFmpegPath = "/bin/ls"
	cfg.FFprobePath = "/bin/ls"
	return &Master{Runner: r, Cfg: cfg}
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
	args := strings.Join(segmentArgs(v, "/i.png", "/a.wav", 1.25, "/o.mp4"), " ")
	if !strings.Contains(args, "pad=1920:1080:(ow-iw)/2:(oh-ih)/2:white") {
		t.Errorf("pad color not applied: %s", args)
	}
	if !strings.Contains(args, "-t 1.250") {
		t.Errorf("duration not applied: %s", args)
	}
}

func TestBuildChapters(t *testing.T) {
	ws := seed(t, twoPages)
	fr := &fakeRunner{dur: "2.000"}
	m := newMaster(fr)

	res, err := m.Build(context.Background(), ws, "deck", twoPages, Options{Chapters: true})
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

	if _, err := m.Build(context.Background(), ws, "deck", pages, Options{Chapters: true}); err != nil {
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
