package health

import (
	"encoding/json"
	"net/http"
)

const defaultServiceName = "server-crawlstars"

type Status struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

func Check(service string) Status {
	if service == "" {
		service = defaultServiceName
	}

	return Status{
		Status:  "ok",
		Service: service,
	}
}

func Handler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Check(service))
	}
}
