package master

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/manifest"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
	"github.com/nlink-jp/video-studio-mcp/internal/workspace"
)

// stderrTailBytes bounds how much ffmpeg stderr is attached to errors.
const stderrTailBytes = 512

// Master renders the final presentation video for a page manifest.
type Master struct {
	Runner Runner
	Cfg    config.VideoConfig
}

// Options control one Build call.
type Options struct {
	OutputName string // basename without extension; default: manifest file stem
	Chapters   bool   // emit one per-page chapter marker in the MP4
}

// Result is the master tool's response payload.
type Result struct {
	MasterPath      string  `json:"master_path"`
	Pages           int     `json:"pages"`
	DurationSeconds float64 `json:"duration_seconds"`
	Chapters        int     `json:"chapters"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	FPS             int     `json:"fps"`
}

// resolved is one page with its workspace-relative asset paths, chapter title,
// and probed audio duration.
type resolved struct {
	imgRel   string
	audioRel string
	title    string
	durSec   float64
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
		items = append(items, resolved{imgRel: imgRel, audioRel: audioRel, title: p.Title})
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
	segRels := make([]string, 0, len(items))
	for i := range items {
		segRel := filepath.Join(tmpRel, fmt.Sprintf("seg_%03d.mp4", i+1))
		args := segmentArgs(m.Cfg, ws.Path(items[i].imgRel), ws.Path(items[i].audioRel), items[i].durSec, ws.Path(segRel))
		if err := m.runFFmpeg(ctx, args); err != nil {
			return Result{}, err
		}
		segRels = append(segRels, segRel)
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

	// 7. Final concat encode (stream copy — segments share codec params).
	name := opts.OutputName
	if name == "" {
		name = manifestStem
	}
	outRel := filepath.Join(workspace.DirOutput, name+".mp4")
	if err := m.runFFmpeg(ctx, concatArgs(ws.Path(listRel), metadataPath, ws.Path(outRel))); err != nil {
		return Result{}, err
	}

	return Result{
		MasterPath:      ws.Path(outRel),
		Pages:           len(items),
		DurationSeconds: totalSec,
		Chapters:        chapterCount,
		Width:           m.Cfg.Width,
		Height:          m.Cfg.Height,
		FPS:             m.Cfg.FPS,
	}, nil
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
