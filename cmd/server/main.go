package main

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"github.com/Second-Loop/Server-CrawlStars/internal/docs"
	"github.com/Second-Loop/Server-CrawlStars/internal/health"
	"github.com/Second-Loop/Server-CrawlStars/internal/rooms"
	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	serverconfig "github.com/Second-Loop/Server-CrawlStars/server-config"
)

const serviceName = "server-crawlstars"

func main() {
	addr := os.Getenv("SERVER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	roomHandlerConfig, err := loadRoomHandlerConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	mux, err := newMux(roomHandlerConfig)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("%s listening on %s", serviceName, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func newMux(roomHandlerConfig rooms.HandlerConfig) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.Handle("/health", health.Handler(serviceName))
	docsHandler := docs.Handler()
	mux.Handle("/openapi", docsHandler)
	mux.Handle("/asyncapi", docsHandler)
	mux.Handle("/openapi.yaml", docsHandler)
	mux.Handle("/asyncapi.yaml", docsHandler)
	store := rooms.NewStoreWithConfig(5, rooms.StoreConfig{GameConfig: loadGameConfig()})
	roomHandler, err := rooms.HandlerWithConfig(store, roomHandlerConfig)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("configure rooms handler: %w", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isRoomHandlerPath(r.URL.Path) {
			roomHandler.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
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

func loadGameConfig() simulation.GameConfig {
	gameConfig, err := simulation.LoadGameConfig(serverconfig.Reader())
	if err != nil {
		log.Printf("failed to load server game config: %v; using static fallback", err)
		return simulation.StaticGameConfig()
	}
	return gameConfig
}
