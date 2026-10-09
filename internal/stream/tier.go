// Package stream decides how a track is delivered at a quality tier and
// produces transcoded files through a bounded, de-duplicated cache.
package stream

import (
	"path/filepath"
	"strings"
)

type Tier string

const (
	Lossless Tier = "lossless"
	High     Tier = "high"
	Saver    Tier = "saver"
)

func ParseTier(s string) (Tier, bool) {
	switch Tier(s) {
	case "", Lossless:
		return Lossless, true
	case High, Saver:
		return Tier(s), true
	}
	return "", false
}

type Source struct {
	Codec       string
	BitrateKbps int
}

type Plan struct {
	Passthrough bool
	Codec       string // aac | flac | alac when transcoding
	BitrateKbps int
	Ext         string
	ContentType string
}

var (
	aac256 = Plan{Codec: "aac", BitrateKbps: 256, Ext: ".m4a", ContentType: "audio/mp4"}
	aac128 = Plan{Codec: "aac", BitrateKbps: 128, Ext: ".m4a", ContentType: "audio/mp4"}
	flac   = Plan{Codec: "flac", Ext: ".flac", ContentType: "audio/flac"}
	alac   = Plan{Codec: "alac", Ext: ".m4a", ContentType: "audio/mp4"}
	pass   = Plan{Passthrough: true}
)

// iOSNative reports codecs AVPlayer plays directly.
func iOSNative(codec string) bool {
	return codec == "mp3" || codec == "aac" || codec == "flac" || codec == "alac"
}

func Decide(src Source, t Tier) Plan {
	c, br := src.Codec, src.BitrateKbps
	lossy := t == High || t == Saver
	switch {
	case c == "mp3" || c == "aac":
		switch t {
		case High:
			if br == 0 || br <= 320 {
				return pass
			}
			return aac256
		case Saver:
			if br > 0 && br <= 160 {
				return pass
			}
			return aac128
		}
		return pass
	case c == "flac" || c == "alac":
		if !lossy {
			return pass
		}
	case c == "wmalossless":
		if !lossy {
			return alac
		}
	case strings.HasPrefix(c, "pcm_") || c == "ape" || c == "wavpack":
		if !lossy {
			return flac
		}
	}
	if t == Saver {
		return aac128
	}
	return aac256 // lossy non-native sources at lossless/high, and lossless sources at high
}

// ContentTypeFor gives the MIME type of an original file served as-is.
func ContentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".mp4":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".flac":
		return "audio/flac"
	}
	return "application/octet-stream"
}

// knownContainer reports whether path's container/extension is one
// ContentTypeFor recognizes, i.e. it's safe to serve the file as-is. A
// codec can be iOS-native (e.g. FLAC) yet muxed into a container (e.g.
// .ogg) that AVPlayer won't open, in which case it must not pass through.
func knownContainer(path string) bool {
	return ContentTypeFor(path) != "application/octet-stream"
}

// forcedPlan gives the transcode Decide would have chosen for src at tier t
// had the codec not been natively playable. Used when Decide says
// Passthrough but the file's actual container isn't one the client can
// open, so the codec-native shortcut has to be overridden.
func forcedPlan(src Source, t Tier) Plan {
	if (src.Codec == "flac" || src.Codec == "alac") && t == Lossless {
		return flac
	}
	if t == Saver {
		return aac128
	}
	return aac256
}
