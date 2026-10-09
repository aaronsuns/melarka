package artwork

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FFmpeg implements Imager with the configured ffmpeg. Nice runs it at low
// CPU priority (config transcode.nice), so cover work never starves playback.
type FFmpeg struct {
	Path string // "" = "ffmpeg"
	Nice bool
}

// command is the argv (program first) that Square runs.
func (f FFmpeg) command(src string, stream int, dst string, size int) []string {
	bin := f.Path
	if bin == "" {
		bin = "ffmpeg"
	}
	m := "0:v:0"
	if stream >= 0 {
		m = "0:" + strconv.Itoa(stream)
	}
	vf := fmt.Sprintf("crop='min(iw,ih)':'min(iw,ih)',scale='trunc(min(%d,iw)/2)*2':-2", size)
	args := []string{bin, "-nostdin", "-v", "error", "-y", "-i", src, "-map", m, "-frames:v", "1",
		"-vf", vf, "-pix_fmt", "yuvj420p", "-q:v", "3", "-update", "1", "-f", "image2", "-c:v", "mjpeg", dst}
	if f.Nice {
		args = append([]string{"nice", "-n", "10"}, args...)
	}
	return args
}

func (f FFmpeg) Square(ctx context.Context, src string, stream int, dst string, size int) error {
	argv := f.command(src, stream, dst, size)
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return nil
}
