package master

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
)

// segmentArgs builds the ffmpeg invocation that renders one page: a still
// image looped for exactly durSec, scaled to fit the target canvas and padded
// (letter/pillar-boxed) to its exact size, muxed with the page audio. Every
// segment is encoded to identical parameters (canvas, fps, pixel format, audio
// codec/rate) so the segments can later be joined by stream copy.
//
// capPath, when non-empty, is a canvas-sized transparent caption PNG composited
// over the page with the core overlay filter (so no libfreetype-enabled ffmpeg
// is required); positioning is already baked into the PNG, hence overlay=0:0.
func segmentArgs(v config.VideoConfig, imgPath, audioPath, capPath string, durSec float64, outPath string) []string {
	scalePad := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:%s,setsar=1,fps=%d",
		v.Width, v.Height, v.Width, v.Height, v.Background, v.FPS,
	)
	args := []string{
		"-y",
		"-loop", "1",
		"-i", imgPath,
		"-i", audioPath,
	}
	if capPath == "" {
		args = append(args, "-t", fmt.Sprintf("%.3f", durSec), "-vf", scalePad)
	} else {
		args = append(args,
			"-i", capPath,
			"-t", fmt.Sprintf("%.3f", durSec),
			"-filter_complex", "[0:v]"+scalePad+"[bg];[bg][2:v]overlay=0:0:format=auto[v]",
			"-map", "[v]",
			"-map", "1:a",
		)
	}
	return append(args,
		"-r", strconv.Itoa(v.FPS),
		"-c:v", "libx264",
		"-preset", v.Preset,
		"-crf", strconv.Itoa(v.CRF),
		"-tune", "stillimage",
		"-pix_fmt", v.PixFmt,
		"-c:a", "aac",
		"-b:a", v.AudioBitrate,
		"-ar", strconv.Itoa(v.AudioSampleRate),
		"-movflags", "+faststart",
		outPath,
	)
}

// concatArgs builds the final concat-demuxer invocation that joins the
// per-page segments by stream copy (they share identical codec parameters).
//
// metadataPath is an ffmetadata chapter file; subtitlePath is an SRT
// closed-caption file. Either or both may be empty. Any extra inputs get
// sequential indices after the concat input (0); the concat streams are always
// mapped, the subtitle is transcoded to mov_text (the MP4 soft-subtitle codec),
// and the chapters are taken as global metadata.
func concatArgs(listPath, metadataPath, subtitlePath, outPath string) []string {
	args := []string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
	}
	idx := 1
	subIdx, metaIdx := -1, -1
	if subtitlePath != "" {
		args = append(args, "-i", subtitlePath)
		subIdx = idx
		idx++
	}
	if metadataPath != "" {
		args = append(args, "-i", metadataPath)
		metaIdx = idx
		idx++
	}
	args = append(args, "-map", "0")
	if subIdx >= 0 {
		args = append(args, "-map", strconv.Itoa(subIdx))
	}
	if metaIdx >= 0 {
		args = append(args, "-map_metadata", strconv.Itoa(metaIdx))
	}
	args = append(args, "-c", "copy")
	if subIdx >= 0 {
		args = append(args, "-c:s", "mov_text")
	}
	return append(args, "-movflags", "+faststart", outPath)
}

// srtTime formats a millisecond offset as an SRT timestamp (HH:MM:SS,mmm).
func srtTime(ms int) string {
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// Chapter is one page's navigation marker (times in milliseconds).
type Chapter struct {
	Title   string
	StartMS int
	EndMS   int
}

// ffmetadata renders the FFMETADATA1 chapter file consumed by concatArgs.
func ffmetadata(chapters []Chapter) string {
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	for _, c := range chapters {
		b.WriteString("[CHAPTER]\n")
		b.WriteString("TIMEBASE=1/1000\n")
		fmt.Fprintf(&b, "START=%d\n", c.StartMS)
		fmt.Fprintf(&b, "END=%d\n", c.EndMS)
		fmt.Fprintf(&b, "title=%s\n", escapeMetadataValue(c.Title))
	}
	return b.String()
}

// escapeMetadataValue escapes the characters the ffmetadata format treats
// specially (=, ;, #, \ and newline).
func escapeMetadataValue(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"=", `\=`,
		";", `\;`,
		"#", `\#`,
		"\n", `\`+"\n",
	)
	return r.Replace(s)
}

// probeArgs builds the ffprobe invocation that prints an audio file's duration
// in seconds (a bare float, no wrapper).
func probeArgs(audioPath string) []string {
	return []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		audioPath,
	}
}

// concatList renders the concat demuxer input file. Single quotes inside paths
// are escaped per the ffmpeg concat demuxer quoting rules.
func concatList(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("file '")
		b.WriteString(strings.ReplaceAll(p, "'", `'\''`))
		b.WriteString("'\n")
	}
	return b.String()
}
