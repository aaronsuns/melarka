package ytdlp

import (
	"regexp"
	"strings"
)

// pathLike matches a whitespace-free token containing a "/", the shape of a
// filesystem path (or, incidentally, a URL) — stripped from LastLine's
// output so a message shown to a member never leaks the server's directory
// layout (e.g. the destination template inside a download error).
var pathLike = regexp.MustCompile(`\S*/\S+`)

// LastLine reduces a yt-dlp failure (msg is normally err.Error(), where err
// came from ExecRunner: "yt-dlp: <go err>: <stderr>") to one short,
// user-facing line: it strips that "yt-dlp: ...: " wrapper, then prefers
// whatever follows the last "ERROR:" marker — yt-dlp's own diagnostic,
// rather than the generic "exit status 1" wrapped around it — falling back
// to the message's last non-empty line when there's no such marker. Strips
// anything path-shaped and caps the result at 200 runes.
//
// This is the one place this sanitisation happens: reused for a failed
// download job's stored error and for a search failure's message, so the
// two surfaces can't drift apart into two slightly different formats.
func LastLine(msg string) string {
	msg = strings.TrimPrefix(msg, "yt-dlp: ")
	// Drop ExecRunner's wrapped Go error (e.g. "exit status 1: "), if any —
	// but only when there's something left afterwards, so a message with no
	// such separator (already just one reason) survives untouched.
	if i := strings.Index(msg, ": "); i >= 0 {
		if rest := msg[i+2:]; strings.TrimSpace(rest) != "" {
			msg = rest
		}
	}
	if i := strings.LastIndex(msg, "ERROR:"); i >= 0 {
		msg = msg[i+len("ERROR:"):]
	} else {
		lines := strings.Split(strings.TrimSpace(msg), "\n")
		msg = lines[len(lines)-1]
	}
	msg = pathLike.ReplaceAllString(msg, "")
	msg = strings.TrimSpace(msg)
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:200])
	}
	return msg
}
