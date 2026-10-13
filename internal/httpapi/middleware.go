package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// SecurityHeaders 加标准安全响应头。HSTS 仅在 TLS 连接上设置。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// MaxBody 限制请求体大小。
func MaxBody(bytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, bytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ipRateLimiter 按 IP 限流。限流器按需创建，定期清理过期项。
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	burst    int
	lastSeen map[string]time.Time
}

func newIPRateLimiter(perMinute, burst int) *ipRateLimiter {
	if perMinute <= 0 {
		perMinute = 120
	}
	if burst <= 0 {
		burst = perMinute
	}
	l := &ipRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		lastSeen: make(map[string]time.Time),
		r:        rate.Every(time.Minute / time.Duration(perMinute)),
		burst:    burst,
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			l.mu.Lock()
			for ip, seen := range l.lastSeen {
				if time.Since(seen) > 2*time.Hour {
					delete(l.limiters, ip)
					delete(l.lastSeen, ip)
				}
			}
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(l.r, l.burst)
		l.limiters[ip] = lim
	}
	l.lastSeen[ip] = time.Now()
	return lim.Allow()
}

func (l *ipRateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit exceeded"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimit 全局限流中间件。
func RateLimit(perMinute int) func(http.Handler) http.Handler {
	l := newIPRateLimiter(perMinute, perMinute)
	return l.middleware
}

// RateLimitStrict 供登录等敏感端点使用的独立限流器。
func RateLimitStrict(perMinute int) func(http.Handler) http.Handler {
	l := newIPRateLimiter(perMinute, perMinute)
	return l.middleware
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

var _ = strconv.Itoa
