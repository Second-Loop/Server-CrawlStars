package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry         *prometheus.Registry
	activeRooms      prometheus.Gauge
	connectedClients prometheus.Gauge
	tickDuration     prometheus.Histogram
	webSocketCloses  *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	metrics := &Metrics{
		registry: registry,
		activeRooms: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "crawlstars_active_rooms",
			Help: "Current number of active rooms.",
		}),
		connectedClients: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "crawlstars_connected_clients",
			Help: "Current number of connected WebSocket clients.",
		}),
		tickDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "crawlstars_tick_duration_seconds",
			Help: "Room simulation tick duration in seconds.",
		}),
		webSocketCloses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "crawlstars_websocket_closes_total",
			Help: "WebSocket sessions closed by bounded close cause.",
		}, []string{"cause"}),
	}
	registry.MustRegister(metrics.activeRooms, metrics.connectedClients, metrics.tickDuration, metrics.webSocketCloses)
	return metrics
}

func (m *Metrics) SetActiveRooms(count int) {
	m.activeRooms.Set(float64(count))
}

func (m *Metrics) SetConnectedClients(count int) {
	m.connectedClients.Set(float64(count))
}

func (m *Metrics) ObserveTick(duration time.Duration) {
	m.tickDuration.Observe(duration.Seconds())
}

func (m *Metrics) ObserveWebSocketClose(cause string) {
	if !boundedWebSocketCloseCause(cause) {
		return
	}
	m.webSocketCloses.WithLabelValues(cause).Inc()
}

func boundedWebSocketCloseCause(cause string) bool {
	switch cause {
	case "peer_close", "read_failure", "write_timeout", "write_error",
		"ping_timeout", "ping_error", "control_overflow", "game_end",
		"prestart_cancel", "expiry", "shutdown", "debug_delete":
		return true
	default:
		return false
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
