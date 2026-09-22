package master

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // image.Decode: page images are PNG or JPEG
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nlink-jp/video-studio-mcp/internal/caption"
	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/manifest"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
	"github.com/nlink-jp/video-studio-mcp/internal/workspace"
)

// stderrTailBytes bounds how much ffmpeg stderr is attached to errors.
const stderrTailBytes = 512

// Master renders the final presentation video for a page manifest.
type Master struct {
	Runner  Runner
	Cfg     config.VideoConfig
	Caption config.CaptionConfig
}

// Options control one Build call.
type Options struct {
	OutputName       string // basename without extension; default: manifest file stem
	Chapters         bool   // emit one per-page chapter marker in the MP4
	Captions         bool   // burn each page's caption into the video (always-on pixels)
	SoftCaptions     bool   // embed each page's caption as a mov_text closed-caption track (toggleable)
	KeepIntermediate bool   // keep output/tmp (segments, caption PNGs) after a successful render
	// OnProgress, if set, is called as the render advances (phase, pages done,
	// pages total). Used by the async job path; nil is a no-op.
	OnProgress func(phase string, done, total int)
}

// Result is the master tool's response payload.
type Result struct {
	MasterPath      string  `json:"master_path"`
	Pages           int     `json:"pages"`
	DurationSeconds float64 `json:"duration_seconds"`
	Chapters        int     `json:"chapters"`
	CaptionsBurned  int     `json:"captions_burned"`
	SoftCaptionCues int     `json:"soft_caption_cues"`
	// FadesApplied is the number of page boundaries rendered as a fade, counted
	// on the outgoing page. A "fade" transition whose fade would be shorter than
	// one frame is dropped and not counted (ADR-0007).
	FadesApplied int `json:"fades_applied"`
	Width        int `json:"width"`
	Height       int `json:"height"`
	FPS          int `json:"fps"`
}

// resolved is one page with its workspace-relative asset paths, chapter title,
// caption text, transition into the next page, and probed audio duration.
type resolved struct {
	imgRel     string
	imgCodec   string // the decoder the image's own bytes call for (png, mjpeg)
	audioRel   string
	title      string
	caption    string
	transition string
	durSec     float64
}

// privateRoot is where each render's private directory is made: the user's
// cache directory, which no agent write lane covers — $TMPDIR is in gem-agent's
// and lagent's write lanes, and a process running as the same user lists it
// (ADR-0009). A test replaces it.
var privateRoot = func() (string, error) {
	c, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "video-studio-mcp", "render"), nil
}

// privateDir makes this render's directory under privateRoot, clearing what
// renders more than a day old left behind (a server killed mid-render cannot
// clean up after itself).
func privateDir() (string, error) {
	root, err := privateRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
				_ = os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	return os.MkdirTemp(root, "render-")
}

// imageDecoder decodes a page image (judged and read inside the workspace root)
// and names the ffmpeg decoder for it: png or mjpeg. Anything that does not
// decode as PNG or JPEG is an error.
func imageDecoder(ws *workspace.Workspace, rel string) (string, error) {
	b, err := ws.ReadFile(rel)
	if err != nil {
		return "", err
	}
	_, format, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	switch format {
	case "png":
		return "png", nil
	case "jpeg":
		return "mjpeg", nil
	}
	return "", fmt.Errorf("decoded as %s", format)
}

// Build validates that every page's image and audio exist in the workspace,
// probes each page's audio duration, renders one segment per page (image
// looped for the audio's length), and concatenates the segments into one MP4.
func (m *Master) Build(ctx context.Context, ws *workspace.Workspace, manifestStem string, pages []manifest.Page, opts Options) (Result, error) {
	if _, err := exec.LookPath(m.Cfg.FFmpegPath); err != nil {
		return Result{}, toolerr.Newf(toolerr.CodeFFmpegNotFound,
			"ffmpeg not found at %q — install it (brew install ffmpeg) or set video.ffmpeg_path", m.Cfg.FFmpegPath)
	}
	if _, err := exec.LookPath(m.Cfg.FFprobePath); err != nil {
		return Result{}, toolerr.Newf(toolerr.CodeFFmpegNotFound,
			"ffprobe not found at %q — it ships with ffmpeg; install it or set video.ffprobe_path", m.Cfg.FFprobePath)
	}

	report := func(phase string, done, total int) {
		if opts.OnProgress != nil {
			opts.OnProgress(phase, done, total)
		}
	}

	// 1. Resolve + verify every referenced image and audio as a real regular
	// file inside the workspace. A symlink out of the workspace surfaces as
	// path_not_allowed immediately; a genuinely missing file is collected and
	// reported together as manifest_incomplete.
	items := make([]resolved, 0, len(pages))
	var missing []map[string]any
	if err := CheckNames(ws, pages); err != nil {
		return Result{}, err
	}
	for i, p := range pages {
		imgRel, err := ws.ResolveInside(p.Image)
		if err != nil {
			return Result{}, err
		}
		audioRel, err := ws.ResolveInside(p.Audio)
		if err != nil {
			return Result{}, err
		}
		imgOK, err := verifyFile(ws, imgRel)
		if err != nil {
			return Result{}, err
		}
		audioOK, err := verifyFile(ws, audioRel)
		if err != nil {
			return Result{}, err
		}
		if !imgOK || !audioOK {
			miss := map[string]any{"page": i + 1}
			if !imgOK {
				miss["image"] = p.Image
			}
			if !audioOK {
				miss["audio"] = p.Audio
			}
			missing = append(missing, miss)
			continue
		}
		// The image is decoded here, once: ffmpeg fed an image it cannot decode
		// — a GIF or a truncated PNG named .png — fails on every looped frame
		// and never ends, its stderr growing without bound. What decodes is
		// PNG or JPEG (the formats this server takes), and that also names
		// the decoder ffmpeg is told to use, whatever the extension says.
		codec, err := imageDecoder(ws, imgRel)
		if err != nil {
			return Result{}, toolerr.Newf(toolerr.CodeInvalidManifest,
				"page %d: image %q is not a PNG or JPEG image that can be read (%v)", i+1, p.Image, err).
				WithDetails(map[string]any{"page": i + 1, "image": p.Image})
		}
		items = append(items, resolved{
			imgRel:     imgRel,
			imgCodec:   codec,
			audioRel:   audioRel,
			title:      p.Title,
			caption:    p.Caption,
			transition: p.Transition,
		})
	}
	if len(missing) > 0 {
		return Result{}, toolerr.Newf(toolerr.CodeManifestIncomplete,
			"%d page(s) reference an image or audio file missing from the workspace — place the assets, then retry", len(missing)).
			WithDetails(map[string]any{"missing": missing})
	}

	// 2. Probe each page's audio duration (governs its segment length).
	totalSec := 0.0
	for i := range items {
		// Verified again immediately before the spawn: step 1 may be minutes
		// ago (async), and a page swapped for a link since is caught here.
		// What this does not close is recorded in ADR-0009 (the workspace
		// swapped mid-render, formats ffmpeg picks from contents).
		if err := ws.VerifyRegular(items[i].audioRel); err != nil {
			return Result{}, err
		}
		dur, err := m.probeDuration(ctx, ws.Path(items[i].audioRel))
		if err != nil {
			return Result{}, err
		}
		items[i].durSec = dur
		totalSec += dur
	}

	// 3. A private directory for everything ffmpeg writes and reads back:
	// caption PNGs, segments, the concat list, chapters, subtitles, and the
	// master itself until it is placed. The workspace is writable by the
	// caller, and a file swapped there during a render — the concat list
	// rewritten while ffmpeg starts, a segment replaced by an ffconcat list, a
	// link planted where ffmpeg will write — steered ffmpeg outside the
	// workspace (measured, ADR-0009). ffmpeg reads only the page inputs from
	// the workspace, with their formats pinned.
	priv, err := privateDir()
	if err != nil {
		return Result{}, toolerr.Newf(toolerr.CodeWorkspaceFailed, "make a private render directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(priv) }()
	tmpRel := filepath.Join(workspace.DirOutput, "tmp")
	if err := ws.RemoveAll(tmpRel); err != nil {
		return Result{}, toolerr.Newf(toolerr.CodeWorkspaceFailed, "clean tmp: %v", err)
	}
	var kept []string // intermediates to copy into output/tmp when asked to keep them
	// On every way out, success or failure, before the private directory goes
	// (deferred calls run last-in, first-out). Best effort: a copy that fails
	// does not fail the render.
	defer func() {
		if !opts.KeepIntermediate {
			return
		}
		for _, name := range kept {
			_ = ws.PlaceFile(filepath.Join(tmpRel, name), filepath.Join(priv, name))
		}
	}()

	// 4. Render one segment per page. ffmpeg cannot inherit os.Root, so the
	// image/audio inputs verified in step 1 are handed over as absolute paths.
	// When captions are enabled, each non-empty caption is rendered to a
	// server-written transparent PNG and composited via overlay.
	report("rendering", 0, len(items))
	segPaths := make([]string, 0, len(items))
	captionsBurned := 0
	fadesApplied := 0
	for i := range items {
		capPath := ""
		if opts.Captions && strings.TrimSpace(items[i].caption) != "" {
			png, err := caption.Render(m.Caption, items[i].caption, m.Cfg.Width, m.Cfg.Height)
			if err != nil {
				return Result{}, toolerr.Newf(toolerr.CodeCaptionFailed, "render caption for page %d: %v", i+1, err)
			}
			name := fmt.Sprintf("cap_%03d.png", i+1)
			capPath = filepath.Join(priv, name)
			if err := os.WriteFile(capPath, png, 0o600); err != nil {
				return Result{}, toolerr.Newf(toolerr.CodeWorkspaceFailed, "write caption: %v", err)
			}
			kept = append(kept, name)
			captionsBurned++
		}
		// A "fade" transition fades out at the end of its own page and in at the
		// start of the next, inside each page's duration — so this page fades in
		// when the *previous* page asked for it. The last page's transition has
		// no boundary to describe and is ignored (ADR-0007).
		fadeIn, fadeOut := fadeDurations(
			m.Cfg.FadeSeconds, items[i].durSec, m.Cfg.FPS,
			i > 0 && isFade(items[i-1].transition),
			i < len(items)-1 && isFade(items[i].transition),
		)
		if fadeOut > 0 {
			fadesApplied++
		}
		segName := fmt.Sprintf("seg_%03d.mp4", i+1)
		segPath := filepath.Join(priv, segName)
		// Verified again immediately before the spawn (see step 2): a render
		// can take minutes, and page N is opened only after page N-1 is done.
		for _, rel := range []string{items[i].imgRel, items[i].audioRel} {
			if err := ws.VerifyRegular(rel); err != nil {
				return Result{}, err
			}
		}
		args := segmentArgs(m.Cfg, ws.Path(items[i].imgRel), items[i].imgCodec, ws.Path(items[i].audioRel), capPath, items[i].durSec, fadeIn, fadeOut, segPath)
		if err := m.runFFmpeg(ctx, args); err != nil {
			return Result{}, err
		}
		segPaths = append(segPaths, segPath)
		kept = append(kept, segName)
		report("rendering", i+1, len(items))
	}

	// 5. Concat list, in the private directory.
	list, err := concatList(segPaths)
	if err != nil {
		return Result{}, toolerr.New(toolerr.CodePathNotAllowed, err.Error())
	}
	listPath := filepath.Join(priv, "concat.txt")
	if err := os.WriteFile(listPath, []byte(list), 0o600); err != nil {
		return Result{}, toolerr.Newf(toolerr.CodeWorkspaceFailed, "write concat list: %v", err)
	}
	kept = append(kept, "concat.txt")

	// 6. Optional per-page chapter markers (ffmetadata, server-written).
	metadataPath := ""
	chapterCount := 0
	if opts.Chapters {
		chapters := pageChapters(items)
		chapterCount = len(chapters)
		metadataPath = filepath.Join(priv, "ffmetadata.txt")
		if err := os.WriteFile(metadataPath, []byte(ffmetadata(chapters)), 0o600); err != nil {
			return Result{}, toolerr.Newf(toolerr.CodeWorkspaceFailed, "write chapters: %v", err)
		}
		kept = append(kept, "ffmetadata.txt")
	}

	// 6b. Optional soft (closed-caption) subtitle track — an SRT muxed as
	// mov_text; toggleable in the player, no burned pixels. Skipped when no page
	// carries a caption.
	subtitlePath := ""
	softCaptionCues := 0
	if opts.SoftCaptions {
		srtText, cues := buildSRT(items)
		softCaptionCues = cues
		if cues > 0 {
			subtitlePath = filepath.Join(priv, "captions.srt")
			if err := os.WriteFile(subtitlePath, []byte(srtText), 0o600); err != nil {
				return Result{}, toolerr.Newf(toolerr.CodeWorkspaceFailed, "write subtitles: %v", err)
			}
			kept = append(kept, "captions.srt")
		}
	}

	// 7. Final concat encode (stream copy — segments share codec params), into
	// the private directory; then the master is placed in the workspace through
	// its root, replacing whatever is at output/<name>.mp4 rather than writing
	// through it.
	name := opts.OutputName
	if name == "" {
		name = manifestStem
	}
	outRel := filepath.Join(workspace.DirOutput, name+".mp4")
	report("finalizing", len(items), len(items))
	rendered := filepath.Join(priv, "master.mp4")
	if err := m.runFFmpeg(ctx, concatArgs(listPath, metadataPath, subtitlePath, rendered)); err != nil {
		return Result{}, err
	}
	if err := ws.PlaceFile(outRel, rendered); err != nil {
		return Result{}, err
	}

	return Result{
		MasterPath:      ws.Path(outRel),
		Pages:           len(items),
		DurationSeconds: totalSec,
		Chapters:        chapterCount,
		CaptionsBurned:  captionsBurned,
		SoftCaptionCues: softCaptionCues,
		FadesApplied:    fadesApplied,
		Width:           m.Cfg.Width,
		Height:          m.Cfg.Height,
		FPS:             m.Cfg.FPS,
	}, nil
}

// isFade reports whether a manifest transition value asks for a fade. Every
// other accepted value ("", "cut") is a hard cut.
func isFade(transition string) bool { return transition == manifest.TransitionFade }

// buildSRT renders an SRT closed-caption track from the pages that carry a
// caption, timed by the accumulated per-page durations (the same timeline as
// chapters). Pages without a caption advance the clock but emit no cue.
// Returns the SRT text and the number of cues.
func buildSRT(items []resolved) (string, int) {
	var b strings.Builder
	cursorMS := 0
	cues := 0
	for _, it := range items {
		startMS := cursorMS
		cursorMS += int(it.durSec * 1000)
		if strings.TrimSpace(it.caption) == "" {
			continue
		}
		cues++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", cues, srtTime(startMS), srtTime(cursorMS), it.caption)
	}
	return b.String(), cues
}

// pageChapters derives one chapter per page from the accumulated segment
// durations. Segment length is the probed audio duration passed to ffmpeg via
// -t, so these boundaries match the page transitions (within a frame's
// worth of AAC/fps quantization). Empty titles fall back to "Page N".
func pageChapters(items []resolved) []Chapter {
	chs := make([]Chapter, 0, len(items))
	cursor := 0
	for i := range items {
		start := cursor
		end := cursor + int(items[i].durSec*1000)
		title := items[i].title
		if title == "" {
			title = fmt.Sprintf("Page %d", i+1)
		}
		chs = append(chs, Chapter{Title: title, StartMS: start, EndMS: end})
		cursor = end
	}
	return chs
}

// CheckNames refuses a page whose path ffmpeg would read as a pattern rather
// than a file. ffmpeg's image input expands %d into a numbered sequence, and
// globs only after an unescaped %, so a regular file named p%d.png passes
// every check here while ffmpeg opens p0.png, p1.png, … — files nobody judged,
// whose existence its error would then report. The whole path reaches ffmpeg,
// so the workspace's own path is checked too (a work_dir holding %). The
// master tool calls it on the call, and Build again before any spawn.
func CheckNames(ws *workspace.Workspace, pages []manifest.Page) error {
	if strings.Contains(ws.BaseDir, "%") {
		return toolerr.Newf(toolerr.CodePathNotAllowed,
			"the workspace path %q contains %%, which ffmpeg reads as a pattern over other files; use a work_dir without it", ws.BaseDir)
	}
	for _, p := range pages {
		for _, name := range []string{p.Image, p.Audio} {
			if strings.Contains(name, "%") {
				return toolerr.Newf(toolerr.CodePathNotAllowed,
					"%q contains %%, which ffmpeg reads as a pattern over other files; rename the file", name)
			}
		}
	}
	return nil
}

// verifyFile reports whether rel is a usable regular file. A missing file
// returns (false, nil) so the caller can collect it; a symlink / non-regular
// entry returns (false, path_not_allowed) so the caller surfaces it.
func verifyFile(ws *workspace.Workspace, rel string) (bool, error) {
	err := ws.VerifyRegular(rel)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
		return false, err
	}
	return false, nil
}

func (m *Master) probeDuration(ctx context.Context, audioPath string) (float64, error) {
	stdout, stderr, code, err := m.Runner.Run(ctx, m.Cfg.FFprobePath, probeArgs(audioPath))
	if err != nil && code <= 0 {
		return 0, toolerr.Newf(toolerr.CodeProbeFailed, "ffprobe did not start: %v", err)
	}
	if code != 0 {
		return 0, toolerr.Newf(toolerr.CodeProbeFailed, "ffprobe exited with code %d", code).
			WithDetails(map[string]any{"exit_code": code, "stderr_tail": tail(stderr)})
	}
	s := strings.TrimSpace(string(stdout))
	dur, perr := strconv.ParseFloat(s, 64)
	if perr != nil || dur <= 0 {
		return 0, toolerr.Newf(toolerr.CodeProbeFailed,
			"could not read a positive audio duration from ffprobe output %q", s)
	}
	return dur, nil
}

func (m *Master) runFFmpeg(ctx context.Context, args []string) error {
	_, stderr, code, err := m.Runner.Run(ctx, m.Cfg.FFmpegPath, args)
	if err != nil && code <= 0 {
		return toolerr.Newf(toolerr.CodeFFmpegFailed, "ffmpeg did not start: %v", err)
	}
	if code != 0 {
		return toolerr.Newf(toolerr.CodeFFmpegFailed, "ffmpeg exited with code %d", code).
			WithDetails(map[string]any{
				"exit_code":   code,
				"stderr_tail": tail(stderr),
				"args":        args,
			})
	}
	return nil
}

func tail(b []byte) string {
	if len(b) > stderrTailBytes {
		b = b[len(b)-stderrTailBytes:]
	}
	return string(b)
}
