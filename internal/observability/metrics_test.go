package observability

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMetricsRegistersExactCollectorsWithoutLabels(t *testing.T) {
	metrics := NewMetrics()
	metrics.SetActiveRooms(2)
	metrics.SetConnectedClients(3)
	metrics.ObserveTick(25 * time.Millisecond)

	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.GetName())
		for _, metric := range family.GetMetric() {
			if labels := metric.GetLabel(); len(labels) != 0 {
				t.Fatalf("metric %q must not have labels, got %v", family.GetName(), labels)
			}
		}
	}
	slices.Sort(names)
	want := []string{
		"crawlstars_active_rooms",
		"crawlstars_connected_clients",
		"crawlstars_tick_duration_seconds",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("expected exact metric families %v, got %v", want, names)
	}
}

func TestMetricsRegistriesAreIsolated(t *testing.T) {
	first := NewMetrics()
	second := NewMetrics()
	first.SetActiveRooms(1)
	second.SetActiveRooms(7)

	if got := gaugeValue(t, first, "crawlstars_active_rooms"); got != 1 {
		t.Fatalf("expected first registry value 1, got %v", got)
	}
	if got := gaugeValue(t, second, "crawlstars_active_rooms"); got != 7 {
		t.Fatalf("expected second registry value 7, got %v", got)
	}
}

func TestMetricsHandlerServesPrometheusText(t *testing.T) {
	metrics := NewMetrics()
	metrics.SetActiveRooms(2)
	metrics.SetConnectedClients(3)
	metrics.ObserveTick(25 * time.Millisecond)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("expected Prometheus text content type, got %q", contentType)
	}
	body := recorder.Body.String()
	for _, fragment := range []string{
		"# TYPE crawlstars_active_rooms gauge",
		"crawlstars_active_rooms 2",
		"# TYPE crawlstars_connected_clients gauge",
		"crawlstars_connected_clients 3",
		"# TYPE crawlstars_tick_duration_seconds histogram",
		"crawlstars_tick_duration_seconds_count 1",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected response body to contain %q, got:\n%s", fragment, body)
		}
	}
}

func gaugeValue(t *testing.T, metrics *Metrics, name string) float64 {
	t.Helper()
	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("metric family %q not found", name)
	return 0
}
