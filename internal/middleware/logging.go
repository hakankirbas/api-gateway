package middleware

import (
	"log"
	"net/http"
	"time"
)

// LoggingMiddleware logs details of every incoming HTTP request.
// It logs the request method, URL path, and the duration it took to process the request.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		defer func() {
			log.Printf("Request: %s %s from %s - Processed in %s",
				r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
		}()

		next.ServeHTTP(w, r)
	})
}
