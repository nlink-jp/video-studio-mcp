package master

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
	audioRel   string
	title      string
	caption    string
	transition string
	durSec     float64
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
		items = append(items, resolved{
			imgRel:     imgRel,
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
		dur, err := m.probeDuration(ctx, ws.Path(items[i].audioRel))
		if err != nil {
			return Result{}, err
		}
		items[i].durSec = dur
		totalSec += dur
	}

	// 3. Fresh tmp area for the per-page segments.
	tmpRel := filepath.Join(workspace.DirOutput, "tmp")
	if err := ws.RemoveAll(tmpRel); err != nil {
		return Result{}, toolerr.Newf(toolerr.CodeWorkspaceFailed, "clean tmp: %v", err)
	}
	if err := ws.MkdirAll(tmpRel); err != nil {
		return Result{}, err
	}

	// 4. Render one segment per page. ffmpeg cannot inherit os.Root, so the
	// image/audio inputs verified in step 1 are handed over as absolute paths.
	// When captions are enabled, each non-empty caption is rendered to a
	// server-written transparent PNG and composited via overlay.
	report("rendering", 0, len(items))
	segRels := make([]string, 0, len(items))
	captionsBurned := 0
	fadesApplied := 0
	for i := range items {
		capPath := ""
		if opts.Captions && strings.TrimSpace(items[i].caption) != "" {
			png, err := caption.Render(m.Caption, items[i].caption, m.Cfg.Width, m.Cfg.Height)
			if err != nil {
				return Result{}, toolerr.Newf(toolerr.CodeCaptionFailed, "render caption for page %d: %v", i+1, err)
			}
			capRel := filepath.Join(tmpRel, fmt.Sprintf("cap_%03d.png", i+1))
			if err := ws.WriteFileAtomic(capRel, png); err != nil {
				return Result{}, err
			}
			capPath = ws.Path(capRel)
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
		segRel := filepath.Join(tmpRel, fmt.Sprintf("seg_%03d.mp4", i+1))
		args := segmentArgs(m.Cfg, ws.Path(items[i].imgRel), ws.Path(items[i].audioRel), capPath, items[i].durSec, fadeIn, fadeOut, ws.Path(segRel))
		if err := m.runFFmpeg(ctx, args); err != nil {
			return Result{}, err
		}
		segRels = append(segRels, segRel)
		report("rendering", i+1, len(items))
	}

	// 5. Concat list (each segment re-verified as a regular file pre-spawn).
	segPaths := make([]string, 0, len(segRels))
	for _, rel := range segRels {
		if err := ws.VerifyRegular(rel); err != nil {
			return Result{}, err
		}
		segPaths = append(segPaths, ws.Path(rel))
	}
	listRel := filepath.Join(tmpRel, "concat.txt")
	if err := ws.WriteFileAtomic(listRel, []byte(concatList(segPaths))); err != nil {
		return Result{}, err
	}

	// 6. Optional per-page chapter markers (ffmetadata, server-written).
	metadataPath := ""
	chapterCount := 0
	if opts.Chapters {
		chapters := pageChapters(items)
		chapterCount = len(chapters)
		metaRel := filepath.Join(tmpRel, "ffmetadata.txt")
		if err := ws.WriteFileAtomic(metaRel, []byte(ffmetadata(chapters))); err != nil {
			return Result{}, err
		}
		metadataPath = ws.Path(metaRel)
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
			subRel := filepath.Join(tmpRel, "captions.srt")
			if err := ws.WriteFileAtomic(subRel, []byte(srtText)); err != nil {
				return Result{}, err
			}
			subtitlePath = ws.Path(subRel)
		}
	}

	// 7. Final concat encode (stream copy — segments share codec params).
	name := opts.OutputName
	if name == "" {
		name = manifestStem
	}
	outRel := filepath.Join(workspace.DirOutput, name+".mp4")
	// ffmpeg cannot inherit os.Root: it opens the output path itself, so a
	// symlink planted at output/<name>.mp4 would be followed and the link's
	// target overwritten with the render. Clear the path through the workspace
	// root first — a root-based remove unlinks the link, never what it points
	// at — so ffmpeg always creates the file fresh. (The per-page segments
	// under output/tmp are already cleared the same way in step 3.)
	if err := ws.RemoveAll(outRel); err != nil {
		return Result{}, err
	}
	report("finalizing", len(items), len(items))
	if err := m.runFFmpeg(ctx, concatArgs(ws.Path(listRel), metadataPath, subtitlePath, ws.Path(outRel))); err != nil {
		return Result{}, err
	}

	// Best-effort cleanup of the intermediates (segments, caption PNGs, concat
	// list) now that the master exists. Left in place on failure (above) to aid
	// debugging, or when the caller opts to keep them.
	if !opts.KeepIntermediate {
		_ = ws.RemoveAll(tmpRel)
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
