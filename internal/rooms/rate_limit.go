package rooms

import (
	"math"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	DefaultJoinRatePerMinute = 10.0
	DefaultJoinBurst         = 4
	maxExactFloatBurst       = int64(1 << 53)
)

type IPRateLimiter struct {
	mu               sync.Mutex
	ratePerMinute    float64
	burst            float64
	now              func() time.Time
	buckets          map[netip.Addr]*ipBucket
	staleAfter       time.Duration
	lastSweepAt      time.Time
	sweepInitialized bool
}

type ipBucket struct {
	tokens    float64
	updatedAt time.Time
	lastSeen  time.Time
}

func NewIPRateLimiter(ratePerMinute float64, burst int, now func() time.Time) *IPRateLimiter {
	if ratePerMinute <= 0 || math.IsNaN(ratePerMinute) || math.IsInf(ratePerMinute, 0) {
		panic("rate per minute must be finite and positive")
	}
	if burst <= 0 || int64(burst) > maxExactFloatBurst {
		panic("burst must be positive and exactly representable")
	}
	if now == nil {
		now = time.Now
	}

	staleAfter := durationForTokens(float64(burst), ratePerMinute)
	if staleAfter < time.Minute {
		staleAfter = time.Minute
	}
	return &IPRateLimiter{
		ratePerMinute: ratePerMinute,
		burst:         float64(burst),
		now:           now,
		buckets:       make(map[netip.Addr]*ipBucket),
		staleAfter:    staleAfter,
	}
}

func (l *IPRateLimiter) Allow(ip netip.Addr) (bool, time.Duration) {
	ip = canonicalIP(ip)
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)
	bucket := l.buckets[ip]
	if bucket == nil {
		bucket = &ipBucket{
			tokens:    l.burst,
			updatedAt: now,
			lastSeen:  now,
		}
		l.buckets[ip] = bucket
	} else {
		l.refill(bucket, now)
		if now.After(bucket.lastSeen) {
			bucket.lastSeen = now
		}
	}

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	return false, durationForTokens(1-bucket.tokens, l.ratePerMinute)
}

func (l *IPRateLimiter) refill(bucket *ipBucket, now time.Time) {
	if !now.After(bucket.updatedAt) {
		return
	}
	elapsed := now.Sub(bucket.updatedAt)
	bucket.tokens = math.Min(l.burst, bucket.tokens+elapsed.Minutes()*l.ratePerMinute)
	bucket.updatedAt = now
}

func (l *IPRateLimiter) sweep(now time.Time) {
	if !l.sweepInitialized {
		l.lastSweepAt = now
		l.sweepInitialized = true
		return
	}
	if now.Before(l.lastSweepAt) || now.Sub(l.lastSweepAt) < l.staleAfter {
		return
	}
	for ip, bucket := range l.buckets {
		if !now.Before(bucket.lastSeen.Add(l.staleAfter)) {
			delete(l.buckets, ip)
		}
	}
	l.lastSweepAt = now
}

func durationForTokens(tokens float64, ratePerMinute float64) time.Duration {
	nanoseconds := tokens / ratePerMinute * float64(time.Minute)
	if math.IsInf(nanoseconds, 1) || nanoseconds >= float64(math.MaxInt64) {
		return time.Duration(math.MaxInt64)
	}
	if nanoseconds <= 1 {
		return time.Nanosecond
	}
	return time.Duration(math.Ceil(nanoseconds))
}

func retryAfterSeconds(wait time.Duration) int64 {
	seconds := int64(wait / time.Second)
	if wait%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func clientIP(r *http.Request, trustedPrefixes []netip.Prefix) netip.Addr {
	peerPort, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	peer := canonicalIP(peerPort.Addr())
	if !isTrustedProxy(peer, trustedPrefixes) {
		return peer
	}

	forwardedValues := r.Header.Values("CF-Connecting-IP")
	if len(forwardedValues) != 1 {
		return peer
	}
	forwarded, err := netip.ParseAddr(strings.TrimSpace(forwardedValues[0]))
	if err != nil {
		return peer
	}
	return canonicalIP(forwarded)
}

func canonicalIP(ip netip.Addr) netip.Addr {
	if !ip.IsValid() {
		return netip.Addr{}
	}
	ip = ip.Unmap()
	if ip.Is6() {
		ip = ip.WithZone("")
	}
	return ip
}

func isTrustedProxy(peer netip.Addr, trustedPrefixes []netip.Prefix) bool {
	for _, prefix := range trustedPrefixes {
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}
