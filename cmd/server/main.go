package main

import (
	"fmt"
	"log"
	"net/http"
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
	mux.Handle("/matchmaking/join", roomHandler)
	mux.Handle("/rooms", roomHandler)
	mux.Handle("/rooms/", roomHandler)
	return mux, nil
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
	return rooms.HandlerConfig{
		EnableDebugAPI: enabled,
		DebugAPIToken:  getenv("DEBUG_API_TOKEN"),
	}, nil
}

func loadGameConfig() simulation.GameConfig {
	gameConfig, err := simulation.LoadGameConfig(serverconfig.Reader())
	if err != nil {
		log.Printf("failed to load server game config: %v; using static fallback", err)
		return simulation.StaticGameConfig()
	}
	return gameConfig
}
