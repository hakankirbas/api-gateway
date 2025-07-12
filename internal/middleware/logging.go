package middleware

import (
	"api-gateway/pkg/logger"
	"net"
	"net/http"
	"strings"
	"time"
)

// StructuredLoggingMiddleware provides comprehensive request/response logging
type StructuredLoggingMiddleware struct {
	logger *logger.Logger
}

// ResponseWriter wrapper to capture status code and response size
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

// NewStructuredLoggingMiddleware creates a new structured logging middleware
func NewStructuredLoggingMiddleware(logger *logger.Logger) *StructuredLoggingMiddleware {
	return &StructuredLoggingMiddleware{
		logger: logger,
	}
}

// Middleware returns the HTTP middleware function
func (m *StructuredLoggingMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Enrich context with correlation and request IDs
		ctx := logger.EnrichContext(r.Context())

		// Extract correlation ID from header if present
		if correlationID := r.Header.Get("X-Correlation-ID"); correlationID != "" {
			ctx = logger.WithCorrelationID(ctx, correlationID)
		}

		// Extract user ID from context/headers if available
		if userID := extractUserID(r); userID != "" {
			ctx = logger.WithUserID(ctx, userID)
		}

		// Update request with enriched context
		r = r.WithContext(ctx)

		// Set correlation ID in response header
		correlationID := logger.GetCorrelationID(ctx)
		w.Header().Set("X-Correlation-ID", correlationID)
		w.Header().Set("X-Request-ID", logger.GetRequestID(ctx))

		// Wrap response writer to capture details
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     200, // Default status code
		}

		// Get client IP
		clientIP := getClientIP(r)

		// Log request start
		contextLogger := m.logger.WithContext(ctx).WithComponent("http")
		contextLogger.Info("Request started", map[string]interface{}{
			"method":     r.Method,
			"path":       r.URL.Path,
			"query":      r.URL.RawQuery,
			"client_ip":  clientIP,
			"user_agent": r.UserAgent(),
			"referer":    r.Referer(),
			"headers":    sanitizeHeaders(r.Header),
		})

		// Process request
		next.ServeHTTP(wrapped, r)

		// Calculate duration
		duration := time.Since(start)

		// Prepare log fields
		fields := map[string]interface{}{
			"app":            "api-gateway",
			"component":      "http",
			"method":         r.Method,
			"path":           r.URL.Path,
			"status_code":    wrapped.statusCode,
			"duration":       duration,
			"correlation_id": logger.GetCorrelationID(ctx),
			"user_id":        logger.GetUserID(ctx),
			"client_ip":      clientIP,
		}

		// Add query parameters if present
		if r.URL.RawQuery != "" {
			fields["query"] = r.URL.RawQuery
		}

		// Log based on status code
		message := "Request completed"
		if wrapped.statusCode >= 500 {
			contextLogger.Error(message, fields)
		} else if wrapped.statusCode >= 400 {
			contextLogger.Warn(message, fields)
		} else {
			contextLogger.Info(message, fields)
		}

		// Log slow requests
		if duration > 5*time.Second {
			contextLogger.Warn("Slow request detected", map[string]interface{}{
				"method":    r.Method,
				"path":      r.URL.Path,
				"duration":  duration,
				"threshold": "5s",
			})
		}
	})
}

// extractUserID extracts user ID from request context or headers
func extractUserID(r *http.Request) string {
	// Try to get from Authorization header (JWT)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		// Here you would parse the JWT and extract user ID
		// For now, return empty string
		return ""
	}

	// Try to get from X-User-ID header
	if userID := r.Header.Get("X-User-ID"); userID != "" {
		return userID
	}

	return ""
}

// getClientIP extracts the real client IP from request
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (most common)
	xForwardedFor := r.Header.Get("X-Forwarded-For")
	if xForwardedFor != "" {
		// Take the first IP if multiple are present
		ips := strings.Split(xForwardedFor, ",")
		return strings.TrimSpace(ips[0])
	}

	// Check X-Real-IP header
	xRealIP := r.Header.Get("X-Real-IP")
	if xRealIP != "" {
		return xRealIP
	}

	// Check CF-Connecting-IP (Cloudflare)
	cfConnectingIP := r.Header.Get("CF-Connecting-IP")
	if cfConnectingIP != "" {
		return cfConnectingIP
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// sanitizeHeaders removes sensitive headers from logging
func sanitizeHeaders(headers http.Header) map[string]string {
	sanitized := make(map[string]string)
	sensitiveHeaders := map[string]bool{
		"authorization": true,
		"cookie":        true,
		"x-api-key":     true,
		"x-auth-token":  true,
	}

	for key, values := range headers {
		lowerKey := strings.ToLower(key)
		if sensitiveHeaders[lowerKey] {
			sanitized[key] = "[REDACTED]"
		} else if len(values) > 0 {
			sanitized[key] = values[0] // Only log first value
		}
	}

	return sanitized
}

// PanicRecoveryMiddleware recovers from panics and logs them
type PanicRecoveryMiddleware struct {
	logger *logger.Logger
}

// NewPanicRecoveryMiddleware creates a new panic recovery middleware
func NewPanicRecoveryMiddleware(logger *logger.Logger) *PanicRecoveryMiddleware {
	return &PanicRecoveryMiddleware{
		logger: logger,
	}
}

// Middleware returns the HTTP middleware function for panic recovery
func (m *PanicRecoveryMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Log the panic with full context
				contextLogger := m.logger.WithContext(r.Context()).WithComponent("panic_recovery")
				contextLogger.Error("Panic recovered", map[string]interface{}{
					"error":      err,
					"method":     r.Method,
					"path":       r.URL.Path,
					"client_ip":  getClientIP(r),
					"user_agent": r.UserAgent(),
				})

				// Return 500 error
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// RequestIDMiddleware ensures every request has a request ID
type RequestIDMiddleware struct{}

// NewRequestIDMiddleware creates a new request ID middleware
func NewRequestIDMiddleware() *RequestIDMiddleware {
	return &RequestIDMiddleware{}
}

// Middleware returns the HTTP middleware function for request IDs
func (m *RequestIDMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Generate request ID if not present
		if logger.GetRequestID(ctx) == "" {
			ctx = logger.WithRequestID(ctx, logger.GenerateRequestID())
		}

		// Generate correlation ID if not present
		if logger.GetCorrelationID(ctx) == "" {
			// Check if provided in header
			if correlationID := r.Header.Get("X-Correlation-ID"); correlationID != "" {
				ctx = logger.WithCorrelationID(ctx, correlationID)
			} else {
				ctx = logger.WithCorrelationID(ctx, logger.GenerateCorrelationID())
			}
		}

		// Set headers
		w.Header().Set("X-Request-ID", logger.GetRequestID(ctx))
		w.Header().Set("X-Correlation-ID", logger.GetCorrelationID(ctx))

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
