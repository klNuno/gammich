package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/klNuno/gammich/internal/api"
	gammichdb "github.com/klNuno/gammich/internal/db"
	"github.com/klNuno/gammich/internal/faststart"
	"github.com/klNuno/gammich/internal/preview"
	"github.com/klNuno/gammich/internal/scan"
	"github.com/klNuno/gammich/internal/staticfs"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	var (
		addr           = flag.String("addr", ":8080", "listen address")
		dbPath         = flag.String("db", "data/gammich.db", "sqlite path")
		videoRoot      = flag.String("videos", "", "root directory containing game folders")
		cacheRoot      = flag.String("cache", "data/cache", "cache root for sprites/thumbs")
		ffmpegPath     = flag.String("ffmpeg", "ffmpeg", "path to ffmpeg binary")
		hwAccel        = flag.String("hwaccel", "", "ffmpeg -hwaccel value (empty = CPU/libdav1d)")
		previewWorkers = flag.Int("preview-workers", 1, "concurrent preview workers (0 disables local generation)")
		ingestToken    = flag.String("ingest-token", "", "shared secret for external preview ingest")
		proxySecret    = flag.String("proxy-secret", "", "shared secret injected by the reverse proxy (X-Gammich-Proxy-Secret)")
		password       = flag.String("password", "", "built-in login password; when set, reads and edits need a session")
		trustedProxies = flag.String("trusted-proxies", "", "comma-separated CIDRs of reverse proxies whose X-Forwarded-For is trusted for login rate limiting")
		trustLoopback  = flag.Bool("trust-loopback", false, "let loopback clients edit without credentials (local dev only)")
		showVersion    = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	// Precedence: explicit flag > env var > flag default.
	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	fromEnv := func(flagName, envName string, apply func(string)) {
		if explicit[flagName] {
			return
		}
		if v := os.Getenv(envName); v != "" {
			apply(v)
		}
	}
	fromEnv("addr", "GAMMICH_ADDR", func(v string) { *addr = v })
	fromEnv("db", "GAMMICH_DB", func(v string) { *dbPath = v })
	fromEnv("videos", "GAMMICH_VIDEOS", func(v string) { *videoRoot = v })
	fromEnv("cache", "GAMMICH_CACHE", func(v string) { *cacheRoot = v })
	fromEnv("ffmpeg", "GAMMICH_FFMPEG", func(v string) { *ffmpegPath = v })
	fromEnv("hwaccel", "GAMMICH_HWACCEL", func(v string) { *hwAccel = v })
	fromEnv("ingest-token", "GAMMICH_INGEST_TOKEN", func(v string) { *ingestToken = v })
	fromEnv("proxy-secret", "GAMMICH_PROXY_SECRET", func(v string) { *proxySecret = v })
	fromEnv("password", "GAMMICH_PASSWORD", func(v string) { *password = v })
	fromEnv("trusted-proxies", "GAMMICH_TRUSTED_PROXIES", func(v string) { *trustedProxies = v })
	fromEnv("trust-loopback", "GAMMICH_TRUST_LOOPBACK", func(v string) {
		if b, err := strconv.ParseBool(v); err == nil {
			*trustLoopback = b
		}
	})
	// Docker secrets: GAMMICH_<NAME>_FILE holds the path of a file with the value.
	fromEnvFile := func(flagName, envName string, dst *string) {
		if explicit[flagName] || *dst != "" {
			return
		}
		if p := os.Getenv(envName); p != "" {
			b, err := os.ReadFile(p)
			if err != nil {
				fmt.Fprintln(os.Stderr, "read", envName+":", err)
				os.Exit(2)
			}
			*dst = strings.TrimSpace(string(b))
		}
	}
	fromEnvFile("ingest-token", "GAMMICH_INGEST_TOKEN_FILE", ingestToken)
	fromEnvFile("proxy-secret", "GAMMICH_PROXY_SECRET_FILE", proxySecret)
	fromEnvFile("password", "GAMMICH_PASSWORD_FILE", password)
	fromEnv("preview-workers", "GAMMICH_PREVIEW_WORKERS", func(v string) {
		if n, err := strconv.Atoi(v); err == nil {
			*previewWorkers = n
		}
	})

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	log.Info("starting gammich", "version", version, "addr", *addr, "videos", *videoRoot)
	switch {
	case *password != "":
		log.Info("auth: built-in password, reads and edits need a login")
	case *proxySecret != "":
		log.Info("auth: reverse proxy secret, reads are open, edits need the proxy")
	default:
		log.Warn("auth: none configured, the library is read-only; set GAMMICH_PASSWORD to enable editing")
	}
	if *trustLoopback {
		log.Warn("GAMMICH_TRUST_LOOPBACK is on: any loopback client can edit; never enable it behind a reverse proxy on this host")
	}

	proxies, err := api.ParseCIDRs(*trustedProxies)
	if err != nil {
		log.Error("invalid GAMMICH_TRUSTED_PROXIES", "err", err)
		os.Exit(2)
	}

	if *videoRoot == "" {
		log.Error("videos root not set; use -videos or GAMMICH_VIDEOS")
		os.Exit(2)
	}

	if err := os.MkdirAll(*cacheRoot+"/sprites", 0o755); err != nil {
		log.Error("mkdir cache sprites", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*cacheRoot+"/thumbs", 0o755); err != nil {
		log.Error("mkdir cache thumbs", "err", err)
		os.Exit(1)
	}

	db, err := gammichdb.Open(*dbPath)
	if err != nil {
		log.Error("db open", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := gammichdb.Migrate(db); err != nil {
		log.Error("db migrate", "err", err)
		os.Exit(1)
	}

	scanner := &scan.Scanner{DB: db, Root: *videoRoot, Log: log}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	srv := &api.Server{
		DB:             db,
		VideoRoot:      *videoRoot,
		CacheRoot:      *cacheRoot,
		StaticFS:       staticfs.FS(),
		Log:            log,
		IngestToken:    *ingestToken,
		ProxySecret:    *proxySecret,
		Password:       *password,
		TrustLoopback:  *trustLoopback,
		TrustedProxies: proxies,
		Version:        version,
		RootCtx:        rootCtx,
		FFmpegPath:     *ffmpegPath,
		OnScan: func(ctx context.Context) (scan.Result, error) {
			return scanner.Run(ctx)
		},
	}

	if *previewWorkers > 0 {
		pw := &preview.Worker{
			DB:         db,
			VideoRoot:  *videoRoot,
			CacheRoot:  *cacheRoot,
			FFmpegPath: *ffmpegPath,
			HWAccel:    *hwAccel,
			Workers:    *previewWorkers,
			Log:        log,
		}
		go pw.Run(rootCtx)
	}

	scanTicker := time.NewTicker(5 * time.Minute)
	defer scanTicker.Stop()
	go func() {
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-scanTicker.C:
				if _, err := scanner.Run(rootCtx); err != nil && !errors.Is(err, scan.ErrAlreadyRunning) {
					log.Warn("periodic scan", "err", err)
				}
			}
		}
	}()

	go func() {
		preview.CleanCache(rootCtx, db, *cacheRoot, log)
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-t.C:
				preview.CleanCache(rootCtx, db, *cacheRoot, log)
			}
		}
	}()

	go faststart.Backfill(rootCtx, db, *videoRoot, log)

	// Audio metadata for clips probed before migration 0011, then hourly for
	// ingested clips and those skipped while the NAS slept.
	go func() {
		preview.BackfillAudio(rootCtx, db, *videoRoot, *ffmpegPath, log)
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-t.C:
				preview.BackfillAudio(rootCtx, db, *videoRoot, *ffmpegPath, log)
			}
		}
	}()

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout intentionally unset: video streaming holds connections
		// far longer than any fixed deadline.
	}

	go func() {
		log.Info("scan on boot")
		if _, err := scanner.Run(rootCtx); err != nil && !errors.Is(err, scan.ErrAlreadyRunning) {
			log.Error("initial scan", "err", err)
		}
	}()

	log.Info("listening", "addr", *addr, "videos", *videoRoot, "cache", *cacheRoot)
	idleConns := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Info("shutdown signal")
		rootCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		close(idleConns)
	}()
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http", "err", err)
		os.Exit(1)
	}
	<-idleConns
}
