package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/docs"
	"github.com/Second-Loop/Server-CrawlStars/internal/health"
	"github.com/Second-Loop/Server-CrawlStars/internal/observability"
	"github.com/Second-Loop/Server-CrawlStars/internal/rooms"
	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	serverconfig "github.com/Second-Loop/Server-CrawlStars/server-config"
)

const (
	serviceName        = "server-crawlstars"
	defaultServerAddr  = "127.0.0.1:8080"
	defaultMetricsAddr = "127.0.0.1:9090"
	shutdownGrace      = 10 * time.Second
)

type runtimeConfig struct {
	serverAddr        string
	metricsAddr       string
	roomHandlerConfig rooms.HandlerConfig
}

type application struct {
	store         *rooms.Store
	metrics       *observability.Metrics
	publicServer  *http.Server
	metricsServer *http.Server
	logger        *slog.Logger
	listen        func(network string, address string) (net.Listener, error)
	shutdownGrace time.Duration
}

type serveResult struct {
	index int
	err   error
}

type shutdownResult struct {
	index int
	err   error
}

func loadRuntimeConfig(getenv func(string) string) (runtimeConfig, error) {
	serverAddr := strings.TrimSpace(getenv("SERVER_ADDR"))
	if serverAddr == "" {
		serverAddr = defaultServerAddr
	}

	metricsAddr, err := parseMetricsAddress(getenv("METRICS_ADDR"))
	if err != nil {
		return runtimeConfig{}, err
	}
	roomHandlerConfig, err := loadRoomHandlerConfig(getenv)
	if err != nil {
		return runtimeConfig{}, err
	}
	return runtimeConfig{
		serverAddr:        serverAddr,
		metricsAddr:       metricsAddr,
		roomHandlerConfig: roomHandlerConfig,
	}, nil
}

func parseMetricsAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultMetricsAddr
	}
	address, err := netip.ParseAddrPort(value)
	if err != nil || !address.Addr().IsLoopback() || address.Addr().Is4In6() || address.Addr().Zone() != "" {
		return "", fmt.Errorf("METRICS_ADDR must be a loopback IP literal with a numeric port")
	}
	return address.String(), nil
}

func main() {
	os.Exit(realMain())
}

func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runMain(ctx, os.Getenv, os.Stdout)
}

func runMain(ctx context.Context, getenv func(string) string, output io.Writer) int {
	if output == nil {
		output = io.Discard
	}
	logger := slog.New(slog.NewJSONHandler(output, nil))
	config, err := loadRuntimeConfig(getenv)
	if err != nil {
		logger.Error("configuration_failed", "error", err.Error())
		return 1
	}
	app, err := newApplicationWithLogger(config, logger)
	if err != nil {
		logger.Error("application_failed", "error", err.Error())
		return 1
	}

	if err := app.Run(ctx); err != nil {
		logger.Error("server_failed", "error", err.Error())
		return 1
	}
	logger.Info("server_stopped")
	return 0
}

func newApplication(config runtimeConfig) (*application, error) {
	return newApplicationWithLogger(config, slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

func newApplicationWithLogger(config runtimeConfig, logger *slog.Logger) (*application, error) {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	metrics := observability.NewMetrics()
	store := rooms.NewStoreWithConfig(5, rooms.StoreConfig{
		GameConfig: loadGameConfig(logger),
		Logger:     logger,
		Observer:   metrics,
	})
	roomHandler, err := rooms.HandlerWithConfig(store, config.roomHandlerConfig)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("configure rooms handler: %w", err)
	}
	errorLog := slog.NewLogLogger(logger.Handler(), slog.LevelError)

	return &application{
		store:   store,
		metrics: metrics,
		publicServer: &http.Server{
			Addr:              config.serverAddr,
			Handler:           newMux(roomHandler),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
			ErrorLog:          errorLog,
		},
		metricsServer: &http.Server{
			Addr:     config.metricsAddr,
			Handler:  newMetricsMux(metrics.Handler()),
			ErrorLog: errorLog,
		},
		logger:        logger,
		listen:        net.Listen,
		shutdownGrace: shutdownGrace,
	}, nil
}

// Run owns both listeners and tears the whole application down when its
// context ends or either HTTP server returns.
func (a *application) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return prioritizeApplicationError(nil, a.shutdown())
	}

	publicListener, err := a.listen("tcp", a.publicServer.Addr)
	if err != nil {
		shutdownErrors := a.shutdown()
		return prioritizeApplicationError([]error{fmt.Errorf("bind public server: %w", err)}, shutdownErrors)
	}
	metricsListener, err := a.listen("tcp", a.metricsServer.Addr)
	if err != nil {
		_ = publicListener.Close()
		shutdownErrors := a.shutdown()
		return prioritizeApplicationError([]error{fmt.Errorf("bind metrics server: %w", err)}, shutdownErrors)
	}
	a.logger.Info(
		"server_listening",
		"public_address", publicListener.Addr().String(),
		"metrics_address", metricsListener.Addr().String(),
	)

	serveResults := make(chan serveResult, 2)
	go func() {
		serveResults <- serveResult{index: 0, err: a.publicServer.Serve(publicListener)}
	}()
	go func() {
		serveResults <- serveResult{index: 1, err: a.metricsServer.Serve(metricsListener)}
	}()

	serveErrors := make([]error, 2)
	completedServe := 0
	select {
	case result := <-serveResults:
		serveErrors[result.index] = result.err
		completedServe++
	case <-ctx.Done():
	}

	shutdownErrors := a.shutdown()
	_ = publicListener.Close()
	_ = metricsListener.Close()
	for completedServe < 2 {
		result := <-serveResults
		serveErrors[result.index] = result.err
		completedServe++
	}
	return prioritizeApplicationError(serveErrors, shutdownErrors)
}

func (a *application) shutdown() []error {
	grace := a.shutdownGrace
	if grace <= 0 {
		grace = shutdownGrace
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	results := make(chan shutdownResult, 3)
	go func() { results <- shutdownResult{index: 0, err: a.store.Shutdown(ctx)} }()
	go func() { results <- shutdownResult{index: 1, err: a.publicServer.Shutdown(ctx)} }()
	go func() { results <- shutdownResult{index: 2, err: a.metricsServer.Shutdown(ctx)} }()
	forceCloseHTTP := func() {
		_ = a.publicServer.Close()
		_ = a.metricsServer.Close()
	}

	return collectShutdownResults(results, ctx.Done(), ctx.Err, forceCloseHTTP)
}

func collectShutdownResults(
	results <-chan shutdownResult,
	deadline <-chan struct{},
	contextError func() error,
	forceCloseHTTP func(),
) []error {
	errorsByOwner := make([]error, 3)
	for remaining := 3; remaining > 0; {
		select {
		case result := <-results:
			errorsByOwner[result.index] = result.err
			remaining--
		case <-deadline:
			forceCloseHTTP()
			deadline = nil
		}
	}
	// Shutdown can return its context error in the same scheduler turn that the
	// deadline channel becomes ready. Force-close again based on final context
	// state so select ordering cannot leave active HTTP transports behind.
	if contextError() != nil {
		forceCloseHTTP()
	}
	return errorsByOwner
}

func prioritizeApplicationError(serveErrors []error, shutdownErrors []error) error {
	var serving []error
	for _, err := range serveErrors {
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			continue
		}
		serving = append(serving, err)
	}
	if len(serving) > 0 {
		return errors.Join(serving...)
	}

	var shutdown []error
	for _, err := range shutdownErrors {
		if err != nil {
			shutdown = append(shutdown, err)
		}
	}
	return errors.Join(shutdown...)
}

func newMux(roomHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", health.Handler(serviceName))
	docsHandler := docs.Handler()
	mux.Handle("/openapi", docsHandler)
	mux.Handle("/asyncapi", docsHandler)
	mux.Handle("/openapi.yaml", docsHandler)
	mux.Handle("/asyncapi.yaml", docsHandler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isRoomHandlerPath(r.URL.Path) {
			roomHandler.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func newMetricsMux(metricsHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		metricsHandler.ServeHTTP(w, r)
	})
}

func isRoomHandlerPath(path string) bool {
	return path == "/rooms" || strings.HasPrefix(path, "/rooms/") ||
		path == "/matchmaking/join" || strings.HasPrefix(path, "/matchmaking/")
}

func loadRoomHandlerConfig(getenv func(string) string) (rooms.HandlerConfig, error) {
	enableValue := strings.TrimSpace(getenv("ENABLE_DEBUG_API"))
	enabled := false
	if enableValue != "" {
		parsed, err := strconv.ParseBool(enableValue)
		if err != nil {
			return rooms.HandlerConfig{}, fmt.Errorf("parse ENABLE_DEBUG_API: %w", err)
		}
		enabled = parsed
	}
	debugToken := getenv("DEBUG_API_TOKEN")
	if enabled && strings.TrimSpace(debugToken) == "" {
		return rooms.HandlerConfig{}, fmt.Errorf("DEBUG_API_TOKEN is required when ENABLE_DEBUG_API is true")
	}

	rateValue := strings.TrimSpace(getenv("MATCHMAKING_JOIN_RATE_PER_MINUTE"))
	burstValue := strings.TrimSpace(getenv("MATCHMAKING_JOIN_BURST"))
	var joinLimiter *rooms.IPRateLimiter
	if rateValue != "" || burstValue != "" {
		ratePerMinute, err := parseJoinRate(rateValue)
		if err != nil {
			return rooms.HandlerConfig{}, err
		}
		burst, err := parseJoinBurst(burstValue)
		if err != nil {
			return rooms.HandlerConfig{}, err
		}
		joinLimiter = rooms.NewIPRateLimiter(ratePerMinute, burst, nil)
	}

	trustedProxyPrefixes, err := parseTrustedProxyPrefixes(getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return rooms.HandlerConfig{}, err
	}
	return rooms.HandlerConfig{
		EnableDebugAPI:       enabled,
		DebugAPIToken:        debugToken,
		JoinLimiter:          joinLimiter,
		TrustedProxyPrefixes: trustedProxyPrefixes,
	}, nil
}

func parseJoinRate(value string) (float64, error) {
	if value == "" {
		return rooms.DefaultJoinRatePerMinute, nil
	}
	ratePerMinute, err := strconv.ParseFloat(value, 64)
	if err != nil || ratePerMinute <= 0 || math.IsNaN(ratePerMinute) || math.IsInf(ratePerMinute, 0) {
		return 0, fmt.Errorf("MATCHMAKING_JOIN_RATE_PER_MINUTE must be finite and positive")
	}
	return ratePerMinute, nil
}

func parseJoinBurst(value string) (int, error) {
	if value == "" {
		return rooms.DefaultJoinBurst, nil
	}
	burst, err := strconv.Atoi(value)
	if err != nil || burst <= 0 || uint64(burst) > uint64(1<<53) {
		return 0, fmt.Errorf("MATCHMAKING_JOIN_BURST must be a positive exact integer")
	}
	return burst, nil
}

func parseTrustedProxyPrefixes(value string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS contains an empty CIDR")
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS contains an invalid CIDR")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func loadGameConfig(logger *slog.Logger) simulation.GameConfig {
	return loadGameConfigFrom(serverconfig.Reader(), logger)
}

func loadGameConfigFrom(reader io.Reader, logger *slog.Logger) simulation.GameConfig {
	gameConfig, err := simulation.LoadGameConfig(reader)
	if err != nil {
		logger.Warn("game_config_fallback", "error", err.Error())
		return simulation.StaticGameConfig()
	}
	return gameConfig
}
