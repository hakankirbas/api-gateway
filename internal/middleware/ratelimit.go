package middleware

import (
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiterMiddleware creates a new middleware for rate limiting.
// It uses a map to store rate limiters for each client (identified by IP address).

// 'r' is the rate limit (tokens per second), e.g., 1 for 1 request per second.
// 'b' is the burst limit (maximum tokens that can be accumulated), e.g., 5 for 5 requests in a burst.
// 'cleanupInterval' is how often the map of clients is cleaned up (e.g., 1 minute).
func RateLimiterMiddleware(l rate.Limit, b int, cleanupInterval time.Duration) func(next http.Handler) http.Handler {
	clients := make(map[string]*client)
	mu := &sync.Mutex{}

	go func() {
		for {
			time.Sleep(cleanupInterval)
			mu.Lock()
			for ip, c := range clients {
				if time.Since(c.lastSeen) > cleanupInterval {
					delete(clients, ip)
					log.Printf("RateLimiter: Cleaned up limiter for IP: %s", ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				log.Printf("RateLimiter: Could not parse remote address %s: %v", r.RemoteAddr, err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			mu.Lock()
			if _, ok := clients[ip]; !ok {
				clients[ip] = &client{limiter: rate.NewLimiter(l, b)}
			}
			clients[ip].lastSeen = time.Now()
			limiter := clients[ip].limiter
			mu.Unlock()

			if !limiter.Allow() {
				log.Printf("RateLimiter: Request from IP %s is rate limited for %s %s", ip, r.Method, r.URL.Path)
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
