package logger

import (
	"context"
	"crypto/rand"
	"fmt"
)

// Context keys for storing metadata
type contextKey string

const (
	correlationIDKey contextKey = "correlation_id"
	requestIDKey     contextKey = "request_id"
	userIDKey        contextKey = "user_id"
	sessionIDKey     contextKey = "session_id"
)

// GenerateCorrelationID generates a new correlation ID
func GenerateCorrelationID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// GenerateRequestID generates a new request ID
func GenerateRequestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("req_%x", b)
}

// WithCorrelationID adds a correlation ID to the context
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, correlationIDKey, correlationID)
}

// GetCorrelationID retrieves the correlation ID from context
func GetCorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}
	return ""
}

// WithRequestID adds a request ID to the context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// GetRequestID retrieves the request ID from context
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// WithUserID adds a user ID to the context
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// GetUserID retrieves the user ID from context
func GetUserID(ctx context.Context) string {
	if id, ok := ctx.Value(userIDKey).(string); ok {
		return id
	}
	return ""
}

// WithSessionID adds a session ID to the context
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey, sessionID)
}

// GetSessionID retrieves the session ID from context
func GetSessionID(ctx context.Context) string {
	if id, ok := ctx.Value(sessionIDKey).(string); ok {
		return id
	}
	return ""
}

// EnrichContext adds correlation and request IDs if they don't exist
func EnrichContext(ctx context.Context) context.Context {
	if GetCorrelationID(ctx) == "" {
		ctx = WithCorrelationID(ctx, GenerateCorrelationID())
	}
	if GetRequestID(ctx) == "" {
		ctx = WithRequestID(ctx, GenerateRequestID())
	}
	return ctx
}
