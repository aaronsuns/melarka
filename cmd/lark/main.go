package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/aaronsuns/lark-server/internal/api"
	"github.com/aaronsuns/lark-server/internal/artwork"
	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/buildinfo"
	"github.com/aaronsuns/lark-server/internal/channels"
	"github.com/aaronsuns/lark-server/internal/config"
	"github.com/aaronsuns/lark-server/internal/db"
	"github.com/aaronsuns/lark-server/internal/download"
	"github.com/aaronsuns/lark-server/internal/lastfm"
	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/loudness"
	"github.com/aaronsuns/lark-server/internal/lyrics"
	"github.com/aaronsuns/lark-server/internal/media"
	"github.com/aaronsuns/lark-server/internal/personal"
	"github.com/aaronsuns/lark-server/internal/prefs"
	"github.com/aaronsuns/lark-server/internal/preview"
	"github.com/aaronsuns/lark-server/internal/radio"
	"github.com/aaronsuns/lark-server/internal/recommend"
	"github.com/aaronsuns/lark-server/internal/stream"
	"github.com/aaronsuns/lark-server/internal/tags"
	"github.com/aaronsuns/lark-server/internal/trash"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println("melarka", buildinfo.Version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("starting", "version", buildinfo.Version)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	d, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer d.Close()

	authStore := &auth.Store{DB: d}
	if err := authStore.EnsureAdmin(ctx, cfg.AdminUser, cfg.AdminPassword); err != nil {
		return err
	}

	libStore := &library.Store{DB: d}
	if err := library.SeedIfEmpty(ctx, libStore, cfg.Libraries); err != nil {
		return err
	}
	vocab := tags.Vocabulary()
	if err := tags.ValidateRules(cfg.Tagging.FolderRules, vocab); err != nil {
		return fmt.Errorf("config tagging.folder_rules: %w", err)
	}
	provs, err := lyrics.Build(cfg.Lyrics.Providers, lyrics.Deps{Tags: media.FFprobe{Path: cfg.FFprobePath}})
	if err != nil {
		return fmt.Errorf("config lyrics.providers: %w", err)
	}
	lyricsSvc := &lyrics.Service{DB: d, Library: libStore, Providers: provs, Log: log}
	artProvs, err := artwork.Build(cfg.Artwork.Providers, artwork.Deps{Probe: media.FFprobe{Path: cfg.FFprobePath}})
	if err != nil {
		return fmt.Errorf("config artwork.providers: %w", err)
	}
	// Niced like transcodes (the NUC's CPU is shared with playback); the
	// service itself runs at most 2 lookups — so 2 ffmpeg processes — at once.
	artworkSvc := &artwork.Service{DB: d, Library: libStore, Providers: artProvs,
		Imager: artwork.FFmpeg{Path: cfg.FFmpegPath, Nice: cfg.Transcode.Nice},
		Dir:    filepath.Join(cfg.CacheDir(), "artwork"), Log: log}
	tagStore := &tags.Store{DB: d, OnChange: libStore.Reindex}
	folder := &tags.FolderTagger{Store: tagStore, Rules: cfg.Tagging.FolderRules, Vocab: vocab}
	scans := &library.Service{Store: libStore, Scanner: &library.Scanner{Store: libStore, Prober: media.FFprobe{Path: cfg.FFprobePath}, Log: log, Tagger: folder}, Log: log}
	backfillDone := make(chan struct{})
	go func() {
		defer close(backfillDone)
		n, err := folder.Backfill(ctx)
		log.Info("folder rules backfill", "tracks", n, "err", err)
		// Garbled (mojibake / placeholder) tags stored before cleaning
		// existed: repaired once, after the folder pass so the two don't
		// compete for the write lock.
		n, err = libStore.RepairStoredTags(ctx)
		log.Info("garbled tags backfill", "tracks", n, "err", err)
	}()
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		if err := scans.Run(ctx); err != nil {
			log.Error("scan service", "err", err)
		}
	}()

	cache := stream.NewCache(filepath.Join(cfg.CacheDir(), "transcode"), cfg.Transcode.CacheMaxBytes,
		cfg.Transcode.MaxConcurrent, stream.ExecRunner{FFmpeg: cfg.FFmpegPath, Nice: cfg.Transcode.Nice})
	go cache.Evict()
	streamSvc := &stream.Service{Library: libStore, Cache: cache, Log: log}
	preparer := stream.NewPreparer(streamSvc, cfg.FFmpegPath)
	prepareDone := make(chan struct{})
	go func() { defer close(prepareDone); preparer.Run(ctx) }()

	trashSvc := &trash.Service{DB: d, Library: libStore, Log: log}
	go trashSvc.Run(ctx)

	// ytdlpBin is the data-dir copy of yt-dlp an admin update writes to.
	// Resolving it fresh on every call (rather than once at startup) means an
	// update that just wrote this file is picked up by the very next
	// download or search without restarting the server.
	ytdlpBin := filepath.Join(cfg.DataDir, "bin", "yt-dlp")
	ytBin := func() string {
		if st, err := os.Stat(ytdlpBin); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return ytdlpBin
		}
		return cfg.YtDlpPath
	}
	ytClient := &ytdlp.Client{Runner: ytdlp.ExecRunner{Bin: ytBin}, CacheDir: filepath.Join(cfg.CacheDir(), "yt-dlp")}
	downloads := &download.Service{DB: d, YT: ytClient, Library: libStore, Scans: scans, Log: log, Workers: 2}
	downloadsDone := make(chan struct{})
	go func() {
		defer close(downloadsDone)
		downloads.Run(ctx)
	}()
	// Downloads tagged before the artist cleanup (原唱的歌曲, 演唱 … around the
	// name): fixed once, where nobody edited the name since.
	artistFixDone := make(chan struct{})
	go func() {
		defer close(artistFixDone)
		n, err := downloads.FixArtistNames(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("download artist names backfill", "tracks", n, "err", err)
		} else {
			log.Info("download artist names backfill", "tracks", n, "err", err)
		}
	}()

	prefetchDone := make(chan struct{})
	go func() { defer close(prefetchDone); lyricsSvc.RunPrefetch(ctx, cfg.Lyrics.PrefetchInterval) }()
	artworkDone := make(chan struct{})
	go func() { defer close(artworkDone); artworkSvc.RunPrefetch(ctx, cfg.Artwork.PrefetchInterval) }()
	// Loudness: one niced ffmpeg decode at a time, waiting while the stream
	// preparer transcodes so playback always comes first.
	loudnessDone := make(chan struct{})
	if cfg.Loudness.Enabled {
		lw := &loudness.Worker{DB: d, Measure: loudness.FFmpeg{Path: cfg.FFmpegPath, Nice: cfg.Transcode.Nice}, Log: log,
			Gap: cfg.Loudness.Gap, RetryFailedAfter: cfg.Loudness.RetryFailedAfter, Busy: func() bool { return !preparer.Idle() }}
		go func() { defer close(loudnessDone); lw.Run(ctx) }()
	} else {
		close(loudnessDone)
	}

	lastfmDone := make(chan struct{})
	// One client (one rate limit) for tagging and recommendations.
	var lastfmClient *lastfm.Client
	if cfg.LastFMAPIKey != "" {
		lastfmClient = &lastfm.Client{APIKey: cfg.LastFMAPIKey}
		w := &lastfm.Worker{DB: d, Source: lastfmClient, Tags: tagStore, Vocab: vocab,
			MinCount: cfg.Tagging.LastFMMinCount, Log: log}
		go func() { defer close(lastfmDone); w.Run(ctx) }()
	} else {
		close(lastfmDone)
		log.Info("last.fm tagging disabled (LARK_LASTFM_API_KEY not set)")
	}

	srv := &api.Server{
		Auth: authStore, Library: libStore, Stream: streamSvc, Prepare: preparer, Personal: &personal.Store{DB: d},
		Prefs: &prefs.Store{DB: d}, Language: cfg.Language, OfflineCacheOff: !cfg.OfflineCache,
		Radio: &radio.Radio{DB: d},
		Tags:  tagStore, Vocab: vocab, Lyrics: lyricsSvc, Artwork: artworkSvc, Trash: trashSvc, Scans: scans, MusicRoot: cfg.MusicRoot, Log: log,
		Downloads: downloads, YT: ytClient, YtDlpBin: ytdlpBin, YtDlpPath: cfg.YtDlpPath,
	}
	recsDone := make(chan struct{})
	if cfg.Recommendations.Enabled {
		recs := &recommend.Service{DB: d, Library: libStore, Gate: srv.SearchGate(), Log: log, RefreshAt: cfg.Recommendations.RefreshAt,
			// Its own niced yt-dlp (same binary), so background work never competes with playback.
			YT: &ytdlp.Client{Runner: ytdlp.ExecRunner{Bin: ytBin, Nice: true}, CacheDir: ytClient.CacheDir}}
		if lastfmClient != nil {
			recs.LastFM = lastfmClient
		}
		srv.Recs = recs
		go func() { defer close(recsDone); recs.Run(ctx) }()
	} else {
		close(recsDone)
		log.Info("recommendations disabled (recommendations.enabled: false)")
	}
	channelsDone := make(chan struct{})
	if cfg.Channels.Enabled {
		ch := &channels.Service{DB: d, Feeds: &channels.FeedClient{BaseURL: cfg.YouTubeFeedURL}, Gate: srv.SearchGate(),
			// Its own niced yt-dlp (same binary), like recommendations: episodes never compete with playback.
			YT:   &ytdlp.Client{Runner: ytdlp.ExecRunner{Bin: ytBin, Nice: true}, CacheDir: ytClient.CacheDir},
			Root: cfg.ChannelsRoot(), PollInterval: cfg.Channels.PollInterval, KeepDays: cfg.Channels.KeepDays,
			InitialBackfill: cfg.Channels.InitialBackfill, MaxBytes: int64(cfg.Channels.MaxGB) << 30,
			RefreshAt: cfg.Recommendations.RefreshAt, Log: log}
		// Previews are interactive: the plain (not niced) yt-dlp, their own two slots.
		// The root is absolute: yt-dlp's reported path must be (make run uses ./data).
		previewRoot, err := filepath.Abs(cfg.PreviewRoot())
		if err != nil {
			return err
		}
		pv := &preview.Service{DB: d, YT: ytClient, Music: downloads, Episodes: ch, Root: previewRoot,
			TTL: cfg.Channels.PreviewTTL, MaxBytes: int64(cfg.Channels.PreviewMaxGB) << 30,
			Remux: preview.FFmpegRemux(cfg.FFmpegPath), Log: log}
		// Episodes are the lowest download priority: music downloads and previews first.
		ch.Busy = func(ctx context.Context) bool {
			b, err := downloads.Busy(ctx)
			return err != nil || b || pv.Downloading()
		}
		// 视频 thumbnails through Lark (some clients can't reach i.ytimg.com); swept with the previews.
		thumbs := &preview.Thumbs{Dir: filepath.Join(previewRoot, "thumbs"), BaseURL: cfg.YouTubeThumbURL}
		pv.Thumbs = thumbs
		srv.Channels, srv.Previews, srv.Thumbs = ch, pv, thumbs
		go func() {
			defer close(channelsDone)
			var wg sync.WaitGroup
			wg.Go(func() { ch.RunPoller(ctx) })
			wg.Go(func() { ch.RunWorker(ctx) })
			wg.Go(func() { ch.RunRetention(ctx) })
			wg.Go(func() { ch.RunDiscovery(ctx) })
			wg.Go(func() { pv.Run(ctx) })
			wg.Wait()
		}()
	} else {
		close(channelsDone)
		log.Info("channels disabled (channels.enabled: false)")
	}
	// Image yt-dlp wins if newer than the data-dir copy; then one background
	// self-update (YouTube breaks old yt-dlp releases often).
	srv.StartYtDlpMaintenance(ctx)
	hs := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hs.Shutdown(shut)
	}()
	log.Info("listening", "addr", cfg.Listen)
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// The background workers hold a reference to d; wait for all of them to
	// stop before the deferred d.Close() above runs, so shutdown never closes
	// the DB under a running scan, download or tagging pass.
	<-backfillDone
	<-scanDone
	<-downloadsDone
	<-artistFixDone
	<-lastfmDone
	<-recsDone
	<-channelsDone
	<-prefetchDone
	<-artworkDone
	<-loudnessDone
	<-prepareDone
	return nil
}
