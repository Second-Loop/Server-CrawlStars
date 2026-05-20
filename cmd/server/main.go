package main

import (
	"log"
	"net/http"
	"os"

	"github.com/Second-Loop/Server-CrawlStars/internal/health"
)

const serviceName = "server-crawlstars"

func main() {
	addr := os.Getenv("SERVER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	mux := http.NewServeMux()
	mux.Handle("/health", health.Handler(serviceName))

	log.Printf("%s listening on %s", serviceName, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
