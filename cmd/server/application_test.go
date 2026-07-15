package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestRuntimeConfigDefaultsAndRejectsInvalidMetricsAddress(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		config, err := loadRuntimeConfig(mapGetenv(nil))
		if err != nil {
			t.Fatalf("load runtime config: %v", err)
		}
		if config.serverAddr != "127.0.0.1:8080" {
			t.Fatalf("expected default server address, got %q", config.serverAddr)
		}
		if config.metricsAddr != "127.0.0.1:9090" {
			t.Fatalf("expected default metrics address, got %q", config.metricsAddr)
		}
	})

	for _, value := range []string{
		"127.0.0.1:9090",
		"127.12.34.56:9191",
		"[::1]:9090",
		" 127.0.0.1:9090 ",
	} {
		t.Run("accept "+value, func(t *testing.T) {
			config, err := loadRuntimeConfig(mapGetenv(map[string]string{"METRICS_ADDR": value}))
			if err != nil {
				t.Fatalf("expected loopback metrics address to be accepted: %v", err)
			}
			if config.metricsAddr == "" {
				t.Fatal("expected normalized metrics address")
			}
		})
	}

	for _, value := range []string{
		":9090",
		"localhost:9090",
		"0.0.0.0:9090",
		"[::]:9090",
		"10.0.0.1:9090",
		"192.168.0.1:9090",
		"100.64.0.1:9090",
		"198.51.100.1:9090",
		"[::ffff:127.0.0.1]:9090",
		"[::1%lo0]:9090",
		"127.0.0.1",
		"127.0.0.1:http",
		"127.0.0.1:65536",
		"::1:9090",
	} {
		t.Run("reject "+value, func(t *testing.T) {
			_, err := loadRuntimeConfig(mapGetenv(map[string]string{"METRICS_ADDR": value}))
			if err == nil {
				t.Fatal("expected invalid metrics address to be rejected")
			}
			if strings.Contains(err.Error(), value) {
				t.Fatal("expected stable metrics configuration error without echoing the input")
			}
		})
	}
}

func TestRuntimeConfigPreservesDebugRateAndTrustedProxySettings(t *testing.T) {
	environment := map[string]string{
		"SERVER_ADDR":                      "127.0.0.1:8181",
		"METRICS_ADDR":                     "[::1]:9191",
		"ENABLE_DEBUG_API":                 "true",
		"DEBUG_API_TOKEN":                  "runtime-config-secret",
		"MATCHMAKING_JOIN_RATE_PER_MINUTE": "10",
		"MATCHMAKING_JOIN_BURST":           "2",
		"TRUSTED_PROXY_CIDRS":              "127.0.0.1/32,::1/128",
	}

	config, err := loadRuntimeConfig(mapGetenv(environment))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	if config.serverAddr != environment["SERVER_ADDR"] || config.metricsAddr != environment["METRICS_ADDR"] {
		t.Fatalf("expected configured addresses, got public=%q metrics=%q", config.serverAddr, config.metricsAddr)
	}
	if !config.roomHandlerConfig.EnableDebugAPI || config.roomHandlerConfig.DebugAPIToken != environment["DEBUG_API_TOKEN"] {
		t.Fatal("expected debug API configuration to be preserved")
	}
	if config.roomHandlerConfig.JoinLimiter == nil {
		t.Fatal("expected matchmaking limiter overrides to be preserved")
	}
	if got := len(config.roomHandlerConfig.TrustedProxyPrefixes); got != 2 {
		t.Fatalf("expected two trusted proxy prefixes, got %d", got)
	}
}

func TestNewApplicationOwnsExactlyOneStoreAndNewMuxDoesNotCreateStore(t *testing.T) {
	config, err := loadRuntimeConfig(mapGetenv(nil))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	app, err := newApplication(config)
	if err != nil {
		t.Fatalf("new application: %v", err)
	}
	t.Cleanup(func() {
		if err := app.store.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown application store: %v", err)
		}
	})

	if app.store == nil || app.metrics == nil {
		t.Fatal("expected application to own one Store and one Metrics registry")
	}
	if app.publicServer == nil || app.metricsServer == nil {
		t.Fatal("expected application to own public and metrics servers")
	}
	if app.publicServer.ErrorLog == nil || app.metricsServer.ErrorLog == nil {
		t.Fatal("expected both HTTP servers to route internal errors through the JSON slog boundary")
	}
	if app.publicServer.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("expected 5s public ReadHeaderTimeout, got %s", app.publicServer.ReadHeaderTimeout)
	}
	if app.publicServer.IdleTimeout != 60*time.Second {
		t.Fatalf("expected 60s public IdleTimeout, got %s", app.publicServer.IdleTimeout)
	}
	if app.publicServer.WriteTimeout != 0 {
		t.Fatalf("expected no global public WriteTimeout, got %s", app.publicServer.WriteTimeout)
	}

	called := false
	roomHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	standaloneMux := newMux(roomHandler)
	recorder := httptest.NewRecorder()
	standaloneMux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/matchmaking/join", nil))
	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("expected injected room handler to own room route, called=%t status=%d", called, recorder.Code)
	}
}

func TestPublicAndPrivateMetricsRouteIsolation(t *testing.T) {
	config, err := loadRuntimeConfig(mapGetenv(nil))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	app, err := newApplicationWithLogger(config, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new application: %v", err)
	}
	t.Cleanup(func() {
		if err := app.store.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown application store: %v", err)
		}
	})

	for _, request := range []struct {
		name    string
		handler http.Handler
		method  string
		path    string
		want    int
	}{
		{name: "public metrics", handler: app.publicServer.Handler, method: http.MethodGet, path: "/metrics", want: http.StatusNotFound},
		{name: "public metrics subtree", handler: app.publicServer.Handler, method: http.MethodGet, path: "/metrics/", want: http.StatusNotFound},
		{name: "private metrics", handler: app.metricsServer.Handler, method: http.MethodGet, path: "/metrics", want: http.StatusOK},
		{name: "private root", handler: app.metricsServer.Handler, method: http.MethodGet, path: "/", want: http.StatusNotFound},
		{name: "private metrics subtree", handler: app.metricsServer.Handler, method: http.MethodGet, path: "/metrics/", want: http.StatusNotFound},
		{name: "private head", handler: app.metricsServer.Handler, method: http.MethodHead, path: "/metrics", want: http.StatusNotFound},
		{name: "private post", handler: app.metricsServer.Handler, method: http.MethodPost, path: "/metrics", want: http.StatusNotFound},
	} {
		t.Run(request.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request.handler.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, nil))
			if recorder.Code != request.want {
				t.Fatalf("expected status %d, got %d", request.want, recorder.Code)
			}
		})
	}
}

func TestApplicationPrebindsBothListenersBeforeServing(t *testing.T) {
	config, err := loadRuntimeConfig(mapGetenv(nil))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	app, err := newApplication(config)
	if err != nil {
		t.Fatalf("new application: %v", err)
	}

	publicListener := newBlockingListener("public")
	bindError := errors.New("metrics bind failed")
	listenCalls := 0
	app.listen = func(_, address string) (net.Listener, error) {
		listenCalls++
		switch listenCalls {
		case 1:
			if address != config.serverAddr {
				t.Fatalf("expected public address %q, got %q", config.serverAddr, address)
			}
			return publicListener, nil
		case 2:
			if address != config.metricsAddr {
				t.Fatalf("expected metrics address %q, got %q", config.metricsAddr, address)
			}
			return nil, bindError
		default:
			t.Fatalf("unexpected listen call %d", listenCalls)
			return nil, errors.New("unreachable")
		}
	}

	runError := app.Run(context.Background())
	if !errors.Is(runError, bindError) {
		t.Fatalf("expected metrics bind error, got %v", runError)
	}
	assertSignalClosed(t, publicListener.closed, "prebound public listener close")
	select {
	case <-publicListener.acceptCalled:
		t.Fatal("public Serve started before the metrics listener was bound")
	default:
	}
	if err := app.store.Shutdown(context.Background()); !errors.Is(err, runError) && err != nil {
		t.Fatalf("expected application Store to already be shut down, got %v", err)
	}
}

func TestAnyServeResultTriggersCoordinatedTeardown(t *testing.T) {
	for _, test := range []struct {
		name       string
		failIndex  int
		serveError error
	}{
		{name: "public failure", failIndex: 0, serveError: errors.New("public serve failed")},
		{name: "metrics failure", failIndex: 1, serveError: errors.New("metrics serve failed")},
		{name: "server closed is a clean trigger", failIndex: 0, serveError: http.ErrServerClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := loadRuntimeConfig(mapGetenv(nil))
			if err != nil {
				t.Fatalf("load runtime config: %v", err)
			}
			app, err := newApplication(config)
			if err != nil {
				t.Fatalf("new application: %v", err)
			}

			listeners := []*blockingListener{newBlockingListener("public"), newBlockingListener("metrics")}
			listeners[test.failIndex].acceptError = test.serveError
			listenIndex := 0
			app.listen = func(_, _ string) (net.Listener, error) {
				listener := listeners[listenIndex]
				listenIndex++
				return listener, nil
			}

			runError := app.Run(context.Background())
			if test.serveError == http.ErrServerClosed {
				if runError != nil {
					t.Fatalf("expected clean ErrServerClosed trigger, got %v", runError)
				}
			} else if !errors.Is(runError, test.serveError) {
				t.Fatalf("expected serve error %v, got %v", test.serveError, runError)
			}
			for index, listener := range listeners {
				assertSignalClosed(t, listener.closed, "listener "+string(rune('0'+index))+" close")
			}
		})
	}
}

func TestApplicationErrorPriority(t *testing.T) {
	serveError := errors.New("serve failure")
	shutdownError := context.DeadlineExceeded

	if got := prioritizeApplicationError([]error{serveError}, []error{shutdownError}); !errors.Is(got, serveError) || errors.Is(got, shutdownError) {
		t.Fatalf("expected serve error to outrank shutdown error, got %v", got)
	}
	if got := prioritizeApplicationError(nil, []error{shutdownError}); !errors.Is(got, shutdownError) {
		t.Fatalf("expected shutdown error without serve error, got %v", got)
	}
	if got := prioritizeApplicationError([]error{http.ErrServerClosed}, nil); got != nil {
		t.Fatalf("expected http.ErrServerClosed to normalize to nil, got %v", got)
	}
	if got := prioritizeApplicationError(nil, nil); got != nil {
		t.Fatalf("expected context-driven clean shutdown to return nil, got %v", got)
	}
}

func TestApplicationCancelAfterPrebindDoesNotLeakListeners(t *testing.T) {
	config, err := loadRuntimeConfig(mapGetenv(nil))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	app, err := newApplication(config)
	if err != nil {
		t.Fatalf("new application: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	listeners := []*blockingListener{newBlockingListener("public"), newBlockingListener("metrics")}
	listenIndex := 0
	app.listen = func(_, _ string) (net.Listener, error) {
		listener := listeners[listenIndex]
		listenIndex++
		if listenIndex == len(listeners) {
			cancel()
		}
		return listener, nil
	}

	if err := app.Run(ctx); err != nil {
		t.Fatalf("expected clean cancellation, got %v", err)
	}
	for index, listener := range listeners {
		assertSignalClosed(t, listener.closed, "listener "+string(rune('0'+index))+" close")
	}
}

func TestApplicationShutdownClosesWebSocketAndBothServersWithinBudget(t *testing.T) {
	config, err := loadRuntimeConfig(mapGetenv(nil))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	app, err := newApplication(config)
	if err != nil {
		t.Fatalf("new application: %v", err)
	}

	blockEntered := make(chan struct{})
	releaseBlock := make(chan struct{})
	originalPublicHandler := app.publicServer.Handler
	app.publicServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shutdown-test-block" {
			originalPublicHandler.ServeHTTP(w, r)
			return
		}
		close(blockEntered)
		<-releaseBlock
		w.WriteHeader(http.StatusNoContent)
	})

	listenerAddresses := make(chan string, 2)
	app.listen = func(network string, _ string) (net.Listener, error) {
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listenerAddresses <- listener.Addr().String()
		return listener, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() {
		select {
		case <-releaseBlock:
		default:
			close(releaseBlock)
		}
	})
	runResult := make(chan error, 1)
	go func() { runResult <- app.Run(ctx) }()

	publicAddress := receiveString(t, listenerAddresses, "public listener address")
	metricsAddress := receiveString(t, listenerAddresses, "metrics listener address")
	waitForHTTPStatus(t, "http://"+publicAddress+"/health", http.StatusOK)
	waitForHTTPStatus(t, "http://"+metricsAddress+"/metrics", http.StatusOK)

	joinResponse, err := http.Post("http://"+publicAddress+"/matchmaking/join", "application/json", nil)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	var joined struct {
		WebSocketPath string `json:"webSocketPath"`
	}
	if err := json.NewDecoder(joinResponse.Body).Decode(&joined); err != nil {
		joinResponse.Body.Close()
		t.Fatalf("decode matchmaking join: %v", err)
	}
	joinResponse.Body.Close()
	if joinResponse.StatusCode != http.StatusCreated {
		t.Fatalf("expected matchmaking status 201, got %d", joinResponse.StatusCode)
	}

	webSocketContext, cancelWebSocket := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelWebSocket()
	connection, response, err := websocket.Dial(webSocketContext, "ws://"+publicAddress+joined.WebSocketPath, nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		t.Fatalf("connect tokenized WebSocket: %v", err)
	}
	defer connection.CloseNow()
	waitForMetricValue(t, app.metricsServer.Handler, "crawlstars_connected_clients", "1")

	type closeResult struct {
		code   websocket.StatusCode
		reason string
		err    error
	}
	webSocketClosed := make(chan closeResult, 1)
	go func() {
		_, _, readErr := connection.Read(context.Background())
		var closeError websocket.CloseError
		if errors.As(readErr, &closeError) {
			webSocketClosed <- closeResult{code: closeError.Code, reason: closeError.Reason, err: readErr}
			return
		}
		webSocketClosed <- closeResult{err: readErr}
	}()

	blockedResponse := make(chan *http.Response, 1)
	blockedError := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + publicAddress + "/shutdown-test-block")
		if err != nil {
			blockedError <- err
			return
		}
		blockedResponse <- response
	}()
	assertSignalClosed(t, blockEntered, "blocking HTTP handler entry")

	shutdownStarted := time.Now()
	cancel()
	waitForDialFailure(t, publicAddress)
	waitForDialFailure(t, metricsAddress)
	close(releaseBlock)

	select {
	case response := <-blockedResponse:
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("expected in-flight request to finish with 204, got %d", response.StatusCode)
		}
	case err := <-blockedError:
		t.Fatalf("in-flight request failed during graceful shutdown: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for in-flight request")
	}

	select {
	case result := <-webSocketClosed:
		if result.code != websocket.StatusNormalClosure || result.reason != "server shutting down" {
			t.Fatalf("expected WebSocket close 1000/server shutting down, got code=%d reason=%q err=%v", result.code, result.reason, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for WebSocket shutdown close")
	}

	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("expected clean application shutdown, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("application shutdown exceeded systemd budget")
	}
	if elapsed := time.Since(shutdownStarted); elapsed >= 15*time.Second {
		t.Fatalf("application shutdown exceeded 15s budget: %s", elapsed)
	}
	waitForMetricValue(t, app.metricsServer.Handler, "crawlstars_active_rooms", "0")
	waitForMetricValue(t, app.metricsServer.Handler, "crawlstars_connected_clients", "0")
}

func TestApplicationShutdownDeadlineAlwaysForceClosesHTTPServers(t *testing.T) {
	config, err := loadRuntimeConfig(mapGetenv(nil))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	app, err := newApplication(config)
	if err != nil {
		t.Fatalf("new application: %v", err)
	}
	app.shutdownGrace = time.Millisecond

	handlerEntered := make(chan struct{})
	handlerExited := make(chan struct{})
	app.publicServer.Handler = http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(handlerEntered)
		<-request.Context().Done()
		close(handlerExited)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for deadline test: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- app.publicServer.Serve(listener) }()
	t.Cleanup(func() {
		_ = app.publicServer.Close()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
			t.Error("public Serve did not stop during cleanup")
		}
	})

	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
		requestDone <- requestErr
	}()
	assertSignalClosed(t, handlerEntered, "deadline test handler entry")

	shutdownErrors := app.shutdown()
	for _, shutdownErr := range shutdownErrors {
		if shutdownErr != nil && !errors.Is(shutdownErr, context.DeadlineExceeded) {
			t.Fatalf("unexpected shutdown error: %v", shutdownErrors)
		}
	}
	select {
	case <-handlerExited:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("HTTP handler transport survived shutdown deadline")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("HTTP client remained blocked after forced shutdown")
	}
}

func TestCollectShutdownResultsForceClosesWhenDeadlineWasNotSelected(t *testing.T) {
	results := make(chan shutdownResult, 3)
	for index := range 3 {
		results <- shutdownResult{index: index}
	}

	forceCloseCalls := 0
	errorsByOwner := collectShutdownResults(
		results,
		make(chan struct{}),
		func() error { return context.DeadlineExceeded },
		func() { forceCloseCalls++ },
	)

	if forceCloseCalls != 1 {
		t.Fatalf("expected one post-loop force close, got %d", forceCloseCalls)
	}
	for index, shutdownErr := range errorsByOwner {
		if shutdownErr != nil {
			t.Fatalf("expected owner %d to finish cleanly, got %v", index, shutdownErr)
		}
	}
}

func TestMainLogsJSONWithoutSecrets(t *testing.T) {
	secret := "raw-Authorization-?token=session-secret"
	for _, test := range []struct {
		name        string
		environment map[string]string
		cancel      bool
		wantCode    int
	}{
		{
			name: "clean canceled runtime",
			environment: map[string]string{
				"ENABLE_DEBUG_API": "true",
				"DEBUG_API_TOKEN":  secret,
			},
			cancel:   true,
			wantCode: 0,
		},
		{
			name: "invalid runtime config",
			environment: map[string]string{
				"DEBUG_API_TOKEN":                  secret,
				"MATCHMAKING_JOIN_RATE_PER_MINUTE": "not-a-rate",
			},
			wantCode: 1,
		},
		{
			name: "listener failure",
			environment: map[string]string{
				"SERVER_ADDR":     "missing-port",
				"DEBUG_API_TOKEN": secret,
			},
			wantCode: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.cancel {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			var output bytes.Buffer
			if code := runMain(ctx, mapGetenv(test.environment), &output); code != test.wantCode {
				t.Fatalf("expected exit code %d, got %d", test.wantCode, code)
			}
			if strings.Contains(output.String(), secret) || strings.Contains(output.String(), "Authorization") || strings.Contains(output.String(), "?token=") {
				t.Fatalf("expected logs to redact runtime secrets, got %s", output.String())
			}
			if test.name == "listener failure" && strings.Contains(output.String(), `"msg":"server_listening"`) {
				t.Fatalf("expected listening event only after both listeners bind, got %s", output.String())
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) == 0 || lines[0] == "" {
				t.Fatal("expected at least one structured log line")
			}
			for _, line := range lines {
				var record map[string]any
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatalf("expected JSON log line, got %q: %v", line, err)
				}
				if record["msg"] == nil {
					t.Fatalf("expected stable event in JSON log line %q", line)
				}
			}
		})
	}
}

func TestHTTPServerErrorLogsUseJSONSlog(t *testing.T) {
	config, err := loadRuntimeConfig(mapGetenv(nil))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	app, err := newApplicationWithLogger(config, logger)
	if err != nil {
		t.Fatalf("new application: %v", err)
	}
	t.Cleanup(app.store.Close)

	app.publicServer.ErrorLog.Print("public internal error")
	app.metricsServer.ErrorLog.Print("metrics internal error")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two JSON error log lines, got %q", output.String())
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("expected JSON HTTP server error log, got %q: %v", line, err)
		}
		if record["level"] != "ERROR" {
			t.Fatalf("expected ERROR level, got %v", record["level"])
		}
	}
}

func mapGetenv(environment map[string]string) func(string) string {
	return func(name string) string {
		return environment[name]
	}
}

type blockingListener struct {
	name         string
	acceptCalled chan struct{}
	closed       chan struct{}
	acceptOnce   sync.Once
	closeOnce    sync.Once
	acceptError  error
}

func newBlockingListener(name string) *blockingListener {
	return &blockingListener{
		name:         name,
		acceptCalled: make(chan struct{}),
		closed:       make(chan struct{}),
	}
}

func (l *blockingListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.acceptCalled) })
	if l.acceptError != nil {
		return nil, l.acceptError
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *blockingListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *blockingListener) Addr() net.Addr {
	return testAddr(l.name)
}

type testAddr string

func (a testAddr) Network() string { return "tcp" }

func (a testAddr) String() string { return string(a) }

func assertSignalClosed(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func receiveString(t *testing.T, values <-chan string, name string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return ""
	}
}

func waitForHTTPStatus(t *testing.T, endpoint string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s status %d", endpoint, want)
}

func waitForMetricValue(t *testing.T, handler http.Handler, name string, value string) {
	t.Helper()
	want := name + " " + value
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if recorder.Code == http.StatusOK && strings.Contains(recorder.Body.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for metric %q", want)
}

func waitForDialFailure(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return
		}
		connection.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listener %s still accepts new connections", address)
}
