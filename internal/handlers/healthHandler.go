package handlers

import (
	"encoding/json"
	"net/http"
	"time"
)

type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
	Version   string    `json:"version,omitempty"`
}

// HealthHandler returns the health status of the API Gateway
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now().UTC(),
		Service:   "api-gateway",
		Version:   "1.0.0",
	}

	json.NewEncoder(w).Encode(response)
}

// ReadinessHandler checks if the gateway is ready to serve traffic
func ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// TODO Add any readiness checks here (database connections, etc.)
	ready := true

	if ready {
		w.WriteHeader(http.StatusOK)
		response := HealthResponse{
			Status:    "ready",
			Timestamp: time.Now().UTC(),
			Service:   "api-gateway",
		}
		json.NewEncoder(w).Encode(response)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		response := HealthResponse{
			Status:    "not ready",
			Timestamp: time.Now().UTC(),
			Service:   "api-gateway",
		}
		json.NewEncoder(w).Encode(response)
	}
}
