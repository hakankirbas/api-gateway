package middleware

import (
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type RateLimiter struct {
	clients         map[string]*client
	mu              sync.Mutex
	limit           rate.Limit
	burst           int
	cleanupInterval time.Duration
}

type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewRateLimiter(limit rate.Limit, burst int, cleanupInterval time.Duration) *RateLimiter {
	rl := &RateLimiter{
		clients:         make(map[string]*client),
		limit:           limit,
		burst:           burst,
		cleanupInterval: cleanupInterval,
	}

	// Start cleanup goroutine
	go rl.cleanup()

	return rl
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(rl.cleanupInterval)
		rl.mu.Lock()
		for ip, c := range rl.clients {
			if time.Since(c.lastSeen) > rl.cleanupInterval {
				delete(rl.clients, ip)
				log.Printf("RateLimiter: Cleaned up limiter for IP: %s", ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			log.Printf("RateLimiter: Could not parse remote address %s: %v", r.RemoteAddr, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		rl.mu.Lock()
		if _, ok := rl.clients[ip]; !ok {
			rl.clients[ip] = &client{limiter: rate.NewLimiter(rl.limit, rl.burst)}
		}
		rl.clients[ip].lastSeen = time.Now()
		limiter := rl.clients[ip].limiter
		rl.mu.Unlock()

		if !limiter.Allow() {
			log.Printf("RateLimiter: Request from IP %s is rate limited for %s %s", ip, r.Method, r.URL.Path)
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
