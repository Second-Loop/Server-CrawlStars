package rooms

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIPRateLimiterBurstAndFractionalRefill(t *testing.T) {
	clock := newRateLimitClock()
	limiter := NewIPRateLimiter(10, 4, clock.Now)
	ip := netip.MustParseAddr("192.0.2.1")
	if limiter.staleAfter != time.Minute {
		t.Fatalf("expected minimum stale duration 1m, got %s", limiter.staleAfter)
	}

	for attempt := 0; attempt < 4; attempt++ {
		allowed, wait := limiter.Allow(ip)
		if !allowed || wait != 0 {
			t.Fatalf("expected burst attempt %d to be allowed", attempt+1)
		}
	}
	if allowed, wait := limiter.Allow(ip); allowed || wait != 6*time.Second {
		t.Fatalf("expected exhausted bucket to wait 6s, got allowed=%t wait=%s", allowed, wait)
	}

	clock.Advance(3 * time.Second)
	if allowed, wait := limiter.Allow(ip); allowed || wait != 3*time.Second {
		t.Fatalf("expected half token to wait 3s, got allowed=%t wait=%s", allowed, wait)
	}
	clock.Advance(3 * time.Second)
	if allowed, wait := limiter.Allow(ip); !allowed || wait != 0 {
		t.Fatalf("expected refilled token to be allowed, got allowed=%t wait=%s", allowed, wait)
	}
}

func TestIPRateLimiterInitializesSweepAtZeroTime(t *testing.T) {
	clock := newRateLimitClock()
	clock.Set(time.Time{})
	limiter := NewIPRateLimiter(10, 4, clock.Now)
	first := netip.MustParseAddr("192.0.2.1")
	second := netip.MustParseAddr("192.0.2.2")

	limiter.Allow(first)
	if !limiter.sweepInitialized || !limiter.lastSweepAt.IsZero() {
		t.Fatal("expected zero time to be a valid initialized sweep timestamp")
	}
	clock.Advance(time.Minute)
	limiter.Allow(second)
	if limiter.lastSweepAt != clock.Now() {
		t.Fatal("expected sweep to run one stale duration after zero time")
	}
}

func TestIPRateLimiterIsolatesAndCanonicalizesAddresses(t *testing.T) {
	clock := newRateLimitClock()
	limiter := NewIPRateLimiter(1, 1, clock.Now)

	first := netip.MustParseAddr("192.0.2.1")
	second := netip.MustParseAddr("192.0.2.2")
	if allowed, _ := limiter.Allow(first); !allowed {
		t.Fatal("expected first IP to be allowed")
	}
	if allowed, _ := limiter.Allow(second); !allowed {
		t.Fatal("expected second IP to have an isolated bucket")
	}
	if allowed, _ := limiter.Allow(netip.MustParseAddr("::ffff:192.0.2.1")); allowed {
		t.Fatal("expected mapped IPv4 to share the canonical IPv4 bucket")
	}

	if allowed, _ := limiter.Allow(netip.MustParseAddr("fe80::1%en0")); !allowed {
		t.Fatal("expected first zoned IPv6 request to be allowed")
	}
	if allowed, _ := limiter.Allow(netip.MustParseAddr("fe80::1%en1")); allowed {
		t.Fatal("expected IPv6 zones to share a canonical bucket")
	}

	if allowed, _ := limiter.Allow(netip.Addr{}); !allowed {
		t.Fatal("expected first invalid address request to use the shared bucket")
	}
	if allowed, _ := limiter.Allow(netip.Addr{}); allowed {
		t.Fatal("expected invalid addresses to share one bucket")
	}
}

func TestIPRateLimiterHandlesClockRollbackFailClosed(t *testing.T) {
	clock := newRateLimitClock()
	start := clock.Now()
	limiter := NewIPRateLimiter(1, 1, clock.Now)
	ip := netip.MustParseAddr("192.0.2.1")

	if allowed, _ := limiter.Allow(ip); !allowed {
		t.Fatal("expected initial request to be allowed")
	}
	clock.Set(start.Add(-time.Minute))
	if allowed, wait := limiter.Allow(ip); allowed || wait != time.Minute {
		t.Fatalf("expected clock rollback not to refill, got allowed=%t wait=%s", allowed, wait)
	}
	clock.Set(start.Add(time.Minute))
	if allowed, wait := limiter.Allow(ip); !allowed || wait != 0 {
		t.Fatalf("expected normal refill after original timestamp, got allowed=%t wait=%s", allowed, wait)
	}
}

func TestIPRateLimiterEvictsOnlyAfterFullRefill(t *testing.T) {
	clock := newRateLimitClock()
	limiter := NewIPRateLimiter(1, 2, clock.Now)
	first := netip.MustParseAddr("192.0.2.1")
	second := netip.MustParseAddr("192.0.2.2")

	limiter.Allow(first)
	wantFirstSweep := clock.Now()
	if !limiter.sweepInitialized || limiter.lastSweepAt != wantFirstSweep {
		t.Fatal("expected first request to initialize the sweep clock")
	}
	if limiter.staleAfter != 2*time.Minute {
		t.Fatalf("expected full-refill stale duration 2m, got %s", limiter.staleAfter)
	}

	clock.Advance(2*time.Minute - time.Second)
	limiter.Allow(second)
	if _, ok := limiter.buckets[first]; !ok {
		t.Fatal("expected bucket to remain before full refill")
	}
	if limiter.lastSweepAt != wantFirstSweep {
		t.Fatal("expected no sweep before stale duration")
	}

	clock.Advance(time.Second)
	limiter.Allow(second)
	if _, ok := limiter.buckets[first]; ok {
		t.Fatal("expected fully refilled idle bucket to be evicted")
	}
	if _, ok := limiter.buckets[second]; !ok {
		t.Fatal("expected recently used bucket to remain")
	}
	if limiter.lastSweepAt != clock.Now() {
		t.Fatal("expected sweep timestamp to advance at threshold")
	}
	if allowed, _ := limiter.Allow(first); !allowed {
		t.Fatal("expected evicted full bucket to be recreated without semantic change")
	}
}

func TestIPRateLimiterConcurrentBurst(t *testing.T) {
	clock := newRateLimitClock()
	const burst = 50
	limiter := NewIPRateLimiter(1, burst, clock.Now)
	ip := netip.MustParseAddr("192.0.2.1")
	var allowed atomic.Int64
	var group sync.WaitGroup

	for range burst * 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			if ok, _ := limiter.Allow(ip); ok {
				allowed.Add(1)
			}
		}()
	}
	group.Wait()
	if got := allowed.Load(); got != burst {
		t.Fatalf("expected exactly %d allowed requests, got %d", burst, got)
	}
}

func TestNewIPRateLimiterRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		rate  float64
		burst int
	}{
		{name: "zero rate", rate: 0, burst: 1},
		{name: "negative rate", rate: -1, burst: 1},
		{name: "NaN rate", rate: math.NaN(), burst: 1},
		{name: "positive infinity rate", rate: math.Inf(1), burst: 1},
		{name: "negative infinity rate", rate: math.Inf(-1), burst: 1},
		{name: "zero burst", rate: 1, burst: 0},
		{name: "negative burst", rate: 1, burst: -1},
		{name: "inexact float burst", rate: 1, burst: int(1<<53) + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected constructor to panic")
				}
			}()
			_ = NewIPRateLimiter(tt.rate, tt.burst, time.Now)
		})
	}
}

func TestClientIPTrustBoundary(t *testing.T) {
	trusted := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	tests := []struct {
		name       string
		remoteAddr string
		cf         []string
		xff        string
		want       string
		wantValid  bool
	}{
		{name: "IPv4 peer", remoteAddr: "198.51.100.10:1234", want: "198.51.100.10", wantValid: true},
		{name: "IPv6 peer", remoteAddr: "[2001:db9::10]:1234", want: "2001:db9::10", wantValid: true},
		{name: "mapped peer", remoteAddr: "[::ffff:198.51.100.10]:1234", want: "198.51.100.10", wantValid: true},
		{name: "untrusted spoof ignored", remoteAddr: "198.51.100.10:1234", cf: []string{"203.0.113.1"}, want: "198.51.100.10", wantValid: true},
		{name: "trusted IPv4 proxy", remoteAddr: "192.0.2.10:1234", cf: []string{"203.0.113.1"}, want: "203.0.113.1", wantValid: true},
		{name: "mapped trusted loopback", remoteAddr: "[::ffff:127.0.0.1]:1234", cf: []string{"203.0.113.2"}, want: "203.0.113.2", wantValid: true},
		{name: "trusted IPv6 proxy", remoteAddr: "[2001:db8::10]:1234", cf: []string{"2001:db9::20"}, want: "2001:db9::20", wantValid: true},
		{name: "forwarded mapped address", remoteAddr: "192.0.2.10:1234", cf: []string{"::ffff:203.0.113.3"}, want: "203.0.113.3", wantValid: true},
		{name: "forwarded zone removed", remoteAddr: "192.0.2.10:1234", cf: []string{"fe80::1%eth0"}, want: "fe80::1", wantValid: true},
		{name: "malformed forwarded falls back", remoteAddr: "192.0.2.10:1234", cf: []string{"not-an-ip"}, want: "192.0.2.10", wantValid: true},
		{name: "multiple forwarded falls back", remoteAddr: "192.0.2.10:1234", cf: []string{"203.0.113.1", "203.0.113.2"}, want: "192.0.2.10", wantValid: true},
		{name: "XFF ignored", remoteAddr: "192.0.2.10:1234", xff: "203.0.113.4", want: "192.0.2.10", wantValid: true},
		{name: "invalid remote ignores forwarded", remoteAddr: "invalid", cf: []string{"203.0.113.1"}},
		{name: "bare IP is invalid", remoteAddr: "198.51.100.10"},
		{name: "hostname is invalid", remoteAddr: "proxy.example:1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/matchmaking/join", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, value := range tt.cf {
				req.Header.Add("CF-Connecting-IP", value)
			}
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			got := clientIP(req, trusted)
			if got.IsValid() != tt.wantValid {
				t.Fatalf("expected validity %t, got %t", tt.wantValid, got.IsValid())
			}
			if tt.wantValid && got.String() != tt.want {
				t.Fatalf("expected client IP %q, got %q", tt.want, got.String())
			}
		})
	}
}

func TestClientIPCanonicalizesMappedTrustedPrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/matchmaking/join", nil)
	req.RemoteAddr = "[::ffff:192.0.2.10]:1234"
	req.Header.Set("CF-Connecting-IP", "203.0.113.10")

	got := clientIP(req, []netip.Prefix{netip.MustParsePrefix("::ffff:192.0.2.0/120")})
	if got.String() != "203.0.113.10" {
		t.Fatalf("expected mapped trusted prefix to accept forwarded client, got %q", got.String())
	}
}

func TestMatchmakingRateLimitUsesDefaultBurst(t *testing.T) {
	store := NewStore(10)
	defer store.Close()
	handler := Handler(store)

	for attempt := 0; attempt < DefaultJoinBurst; attempt++ {
		rec := performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "", "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected burst attempt %d to return 201, got %d", attempt+1, rec.Code)
		}
	}
	assertRateLimited(t, performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "", ""), "")
}

func TestMatchmakingRateLimitReturnsJSONAndRetryAfter(t *testing.T) {
	clock := newRateLimitClock()
	store := NewStore(10)
	defer store.Close()
	handler, err := HandlerWithConfig(store, HandlerConfig{
		JoinLimiter: NewIPRateLimiter(10, 1, clock.Now),
	})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	if rec := performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "", ""); rec.Code != http.StatusCreated {
		t.Fatalf("expected first request to return 201, got %d", rec.Code)
	}
	roomCount := len(store.listRooms().Rooms)
	assertRateLimited(t, performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "", ""), "6")
	if got := len(store.listRooms().Rooms); got != roomCount {
		t.Fatalf("expected rejected join not to mutate rooms, got %d before and %d after", roomCount, got)
	}
}

func TestMatchmakingRateLimitRoundsRetryAfterUpToOneSecond(t *testing.T) {
	clock := newRateLimitClock()
	store := NewStore(10)
	defer store.Close()
	handler, err := HandlerWithConfig(store, HandlerConfig{
		JoinLimiter: NewIPRateLimiter(120, 1, clock.Now),
	})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	_ = performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "", "")
	assertRateLimited(t, performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "", ""), "1")
}

func TestMatchmakingRateLimitOnlyConsumesExactPOST(t *testing.T) {
	clock := newRateLimitClock()
	store := NewStore(10)
	defer store.Close()
	handler, err := HandlerWithConfig(store, HandlerConfig{
		JoinLimiter: NewIPRateLimiter(10, 1, clock.Now),
	})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut} {
		req := httptest.NewRequest(method, "/matchmaking/join", nil)
		req.RemoteAddr = "198.51.100.10:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected %s to return 405, got %d", method, rec.Code)
		}
	}
	if rec := performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "", ""); rec.Code != http.StatusCreated {
		t.Fatalf("expected first POST to return 201, got %d", rec.Code)
	}
	assertRateLimited(t, performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "", ""), "6")
}

func TestMatchmakingRateLimitUsesTrustedClientIP(t *testing.T) {
	clock := newRateLimitClock()
	store := NewStore(10)
	defer store.Close()
	handler, err := HandlerWithConfig(store, HandlerConfig{
		JoinLimiter:          NewIPRateLimiter(10, 1, clock.Now),
		TrustedProxyPrefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
	})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	for _, forwarded := range []string{"203.0.113.1", "203.0.113.2"} {
		rec := performMatchmakingJoinRequest(handler, "192.0.2.10:1234", forwarded, "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected isolated forwarded client to return 201, got %d", rec.Code)
		}
	}
	assertRateLimited(t, performMatchmakingJoinRequest(handler, "192.0.2.10:1234", "203.0.113.1", ""), "6")
}

func TestMatchmakingRateLimitCopiesTrustedProxyPrefixes(t *testing.T) {
	clock := newRateLimitClock()
	store := NewStore(10)
	defer store.Close()
	prefixes := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	handler, err := HandlerWithConfig(store, HandlerConfig{
		JoinLimiter:          NewIPRateLimiter(10, 1, clock.Now),
		TrustedProxyPrefixes: prefixes,
	})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}
	prefixes[0] = netip.MustParsePrefix("198.51.100.0/24")

	for _, forwarded := range []string{"203.0.113.1", "203.0.113.2"} {
		rec := performMatchmakingJoinRequest(handler, "192.0.2.10:1234", forwarded, "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected copied trust boundary to isolate clients, got %d", rec.Code)
		}
	}
}

func TestMatchmakingRateLimitIgnoresUntrustedHeadersAndSharesInvalidPeers(t *testing.T) {
	t.Run("untrusted headers", func(t *testing.T) {
		clock := newRateLimitClock()
		store := NewStore(10)
		defer store.Close()
		handler, err := HandlerWithConfig(store, HandlerConfig{
			JoinLimiter: NewIPRateLimiter(10, 1, clock.Now),
		})
		if err != nil {
			t.Fatalf("create handler: %v", err)
		}

		first := performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "203.0.113.1", "203.0.113.2")
		if first.Code != http.StatusCreated {
			t.Fatalf("expected first request to return 201, got %d", first.Code)
		}
		assertRateLimited(t, performMatchmakingJoinRequest(handler, "198.51.100.10:1234", "203.0.113.3", "203.0.113.4"), "6")
	})

	t.Run("invalid peers", func(t *testing.T) {
		clock := newRateLimitClock()
		store := NewStore(10)
		defer store.Close()
		handler, err := HandlerWithConfig(store, HandlerConfig{
			JoinLimiter:          NewIPRateLimiter(10, 1, clock.Now),
			TrustedProxyPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		})
		if err != nil {
			t.Fatalf("create handler: %v", err)
		}

		first := performMatchmakingJoinRequest(handler, "invalid-one", "203.0.113.1", "")
		if first.Code != http.StatusCreated {
			t.Fatalf("expected first invalid peer to return 201, got %d", first.Code)
		}
		assertRateLimited(t, performMatchmakingJoinRequest(handler, "invalid-two", "203.0.113.2", ""), "6")
	})
}

func performMatchmakingJoinRequest(handler http.Handler, remoteAddr string, forwarded string, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/matchmaking/join", nil)
	req.RemoteAddr = remoteAddr
	if forwarded != "" {
		req.Header.Set("CF-Connecting-IP", forwarded)
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertRateLimited(t *testing.T, rec *httptest.ResponseRecorder, retryAfter string) {
	t.Helper()

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", rec.Code)
	}
	gotRetryAfter := rec.Header().Get("Retry-After")
	if gotRetryAfter == "" {
		t.Fatal("expected Retry-After header")
	}
	if retryAfter != "" && gotRetryAfter != retryAfter {
		t.Fatalf("expected Retry-After %q, got %q", retryAfter, gotRetryAfter)
	}
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode rate limit response: %v", err)
	}
	if body.Error.Code != "rate_limited" {
		t.Fatalf("expected rate_limited error code, got %q", body.Error.Code)
	}
}

type rateLimitClock struct {
	mu  sync.Mutex
	now time.Time
}

func newRateLimitClock() *rateLimitClock {
	return &rateLimitClock{now: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)}
}

func (c *rateLimitClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *rateLimitClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func (c *rateLimitClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}
