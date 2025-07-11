package middleware

import (
	"log"
	"net/http"
	"strings"

	"api-gateway/pkg/jwt"
)

type AuthMiddleware struct {
	jwtService *jwt.Service
}

func NewAuthMiddleware(jwtService *jwt.Service) *AuthMiddleware {
	return &AuthMiddleware{jwtService: jwtService}
}

// AuthMiddleware checks for a valid JWT token in the Authorization header.
// It takes the next http.Handler in the chain and a boolean indicating if auth is required for this specific route.
func (am *AuthMiddleware) Middleware(authRequired bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !authRequired {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				log.Printf("AuthMiddleware: Authorization header missing for %s %s", r.Method, r.URL.Path)
				http.Error(w, "Authorization header required", http.StatusUnauthorized)
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString == authHeader {
				log.Printf("AuthMiddleware: Invalid token format (Bearer token expected) for %s %s", r.Method, r.URL.Path)
				http.Error(w, "Invalid token format (Bearer token expected)", http.StatusUnauthorized)
				return
			}

			err := am.jwtService.VerifyToken(tokenString)
			if err != nil {
				log.Printf("AuthMiddleware: Token verification failed for %s %s: %v", r.Method, r.URL.Path, err)
				http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
