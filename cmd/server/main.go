package main

import (
	"log"
	"net/http"
	"os"

	"github.com/Second-Loop/Server-CrawlStars/internal/docs"
	"github.com/Second-Loop/Server-CrawlStars/internal/health"
	"github.com/Second-Loop/Server-CrawlStars/internal/rooms"
	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

const serviceName = "server-crawlstars"

func main() {
	addr := os.Getenv("SERVER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	mux := newMux()

	log.Printf("%s listening on %s", serviceName, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", health.Handler(serviceName))
	docsHandler := docs.Handler()
	mux.Handle("/openapi", docsHandler)
	mux.Handle("/asyncapi", docsHandler)
	mux.Handle("/openapi.yaml", docsHandler)
	mux.Handle("/asyncapi.yaml", docsHandler)
	roomHandler := rooms.Handler(rooms.NewStoreWithConfig(5, rooms.StoreConfig{Map: loadGameMap()}))
	mux.Handle("/matchmaking/join", roomHandler)
	mux.Handle("/rooms", roomHandler)
	mux.Handle("/rooms/", roomHandler)
	return mux
}

func loadGameMap() simulation.MapData {
	gameMap, err := simulation.LoadDefaultMapFixture()
	if err != nil {
		log.Printf("failed to load map fixture %s: %v; using static fallback", simulation.DefaultMapFixturePath, err)
		return simulation.StaticMapFixture()
	}
	return gameMap
}
