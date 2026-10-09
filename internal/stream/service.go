package stream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aaronsuns/lark-server/internal/library"
)

var ErrUnplayable = errors.New("track cannot be played on this device")

type Service struct {
	Library *library.Store
	Cache   *Cache
	Log     *slog.Logger
}

// plan works out how trackID is delivered at tier t: the plan, its
// transcode-cache key (empty for passthrough) and the source info.
func (s *Service) plan(ctx context.Context, trackID int64, t Tier) (library.StreamInfo, Plan, string, error) {
	si, err := s.Library.StreamInfo(ctx, trackID)
	if err != nil {
		return si, Plan{}, "", err
	}
	src := Source{Codec: si.Codec, BitrateKbps: si.BitrateKbps}
	p := Decide(src, t)
	if p.Passthrough && !knownContainer(si.AbsPath) {
		// The codec itself is iOS-native, but the file's container/extension
		// isn't one AVPlayer recognizes (e.g. FLAC audio muxed into .ogg) —
		// serving it as-is would hand the phone application/octet-stream.
		// Transcode into a recognized container instead.
		p = forcedPlan(src, t)
	}
	if p.Passthrough {
		return si, p, "", nil
	}
	fp := si.Fingerprint
	if len(fp) > 8 {
		fp = fp[:8]
	}
	return si, p, fmt.Sprintf("%d-%s%d-%s", trackID, p.Codec, p.BitrateKbps, fp), nil
}

func (s *Service) Resolve(ctx context.Context, trackID int64, t Tier) (string, string, error) {
	si, p, key, err := s.plan(ctx, trackID, t)
	if err != nil {
		return "", "", err
	}
	if p.Passthrough {
		return si.AbsPath, ContentTypeFor(si.AbsPath), nil
	}
	out, err := s.Cache.Get(ctx, key, si.AbsPath, p)
	if err == nil {
		return out, p.ContentType, nil
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	s.Log.Warn("transcode failed", "track", trackID, "err", err)
	if iOSNative(si.Codec) && knownContainer(si.AbsPath) {
		return si.AbsPath, ContentTypeFor(si.AbsPath), nil
	}
	return "", "", ErrUnplayable
}

// Warm makes sure trackID's transcode for tier t is in the cache, running
// ffmpeg with r when it isn't — only when a transcode slot stays free for
// playback (else ErrBusy). A passthrough track needs nothing: false.
func (s *Service) Warm(ctx context.Context, trackID int64, t Tier, r Runner) (bool, error) {
	si, p, key, err := s.plan(ctx, trackID, t)
	if err != nil || p.Passthrough {
		return false, err
	}
	_, err = s.Cache.TryGetWith(ctx, key, si.AbsPath, p, r)
	return err == nil, err
}
