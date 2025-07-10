package router

import (
	"api-gateway/internal/handlers"
	"api-gateway/internal/middleware"
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

// ProxyRoute represents the structure of our gateway.yaml
type ProxyRoute struct {
	Routes []struct {
		Path         string `yaml:"path"`
		Method       string `yaml:"method"`
		TargetUrl    string `yaml:"target_url"`
		AuthRequired bool   `yaml:"auth_required"`
	} `yaml:"routes"`
}

// HealthManager manages the health status of backend services.
type HealthManager struct {
	statuses      map[string]bool
	mu            sync.RWMutex
	client        *http.Client
	checkInterval time.Duration
	stopCh        chan struct{}
}

func NewHealthManager(interval time.Duration) *HealthManager {
	return &HealthManager{
		statuses:      make(map[string]bool),
		client:        &http.Client{Timeout: 5 * time.Second},
		checkInterval: interval,
		stopCh:        make(chan struct{}),
	}
}

// StartHealthChecks begins monitoring the health of all unique target URLs.
func (hm *HealthManager) StartHealthChecks(routes []struct {
	Path         string `yaml:"path"`
	Method       string `yaml:"method"`
	TargetUrl    string `yaml:"target_url"`
	AuthRequired bool   `yaml:"auth_required"`
}) {
	uniqueTargets := make(map[string]struct{})
	for _, route := range routes {
		uniqueTargets[route.TargetUrl] = struct{}{}
	}

	for targetURL := range uniqueTargets {
		go hm.checkTargetHealth(targetURL)
	}
}

// checkTargetHealth performs periodic health checks for a single target URL.
func (hm *HealthManager) checkTargetHealth(targetURL string) {
	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	log.Printf("Health check started for %s with interval %s", targetURL, hm.checkInterval)

	for {
		select {
		case <-ticker.C:
			hm.performCheck(targetURL)
		case <-hm.stopCh:
			log.Printf("Stopping health check for %s", targetURL)
			return
		}
	}
}

// performCheck sends an HTTP GET request to the target URL and updates its health status.
func (hm *HealthManager) performCheck(targetURL string) {
	healthCheckURL := targetURL + "/health"

	resp, err := hm.client.Get(healthCheckURL)
	isHealthy := false
	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
		resp.Body.Close()
	}

	if err == nil && statusCode >= 200 && statusCode < 400 {
		isHealthy = true
	}

	hm.mu.Lock()
	oldStatus := hm.statuses[targetURL]
	hm.statuses[targetURL] = isHealthy
	hm.mu.Unlock()

	if isHealthy != oldStatus {
		statusStr := "UNHEALTHY"
		if isHealthy {
			statusStr = "HEALTHY"
		}
		log.Printf("Health status for %s changed to %s. (Health Check URL: %s, Status Code: %d, Error: %v)", targetURL, statusStr, healthCheckURL, statusCode, err)
	} else if !isHealthy {
		log.Printf("Health check for %s remains UNHEALTHY. (Health Check URL: %s, Status Code: %d, Error: %v)", targetURL, healthCheckURL, statusCode, err)
	}
}

// IsHealthy checks if a given target URL is currently healthy.
func (hm *HealthManager) IsHealthy(targetURL string) bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.statuses[targetURL]
}

// StopHealthChecks signals all health check goroutines to stop.
func (hm *HealthManager) StopHealthChecks() {
	close(hm.stopCh)
}

// Setup initializes and starts the API Gateway server.
func Setup() {
	pr := getProxyRoutes()

	healthManager := NewHealthManager(10 * time.Second)
	healthManager.StartHealthChecks(pr.Routes)

	r := mux.NewRouter()

	r.Use(middleware.RateLimiterMiddleware(rate.Limit(1), 5, 1*time.Minute))
	r.Use(middleware.LoggingMiddleware)

	pr.registerProxies(r, healthManager)

	port := ":8080"
	log.Printf("API Gateway started on %s", port)

	server := &http.Server{
		Addr:    port,
		Handler: r,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error while initializing API Gateway: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutdown signal received. API Gateway is shutting down...")

	healthManager.StopHealthChecks()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("API Gateway failed to shut down properly: %v", err)
	}

	log.Println("API Gateway was closed successfully.")
}

// registerProxies iterates through the configured routes and registers them with the Mux router.
func (pr *ProxyRoute) registerProxies(r *mux.Router, hm *HealthManager) {
	for _, route := range pr.Routes {
		targetURL, err := url.Parse(route.TargetUrl)
		if err != nil {
			log.Printf("Error parsing target URL %s: %v", route.TargetUrl, err)
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		proxyHandler := func(w http.ResponseWriter, req *http.Request) {
			if !hm.IsHealthy(targetURL.String()) {
				log.Printf("Service %s is unhealthy. Returning 503 for %s %s", targetURL.String(), req.Method, req.URL.Path)
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
				return
			}

			log.Printf("Proxying request: %s %s to %s", req.Method, req.URL.Path, targetURL.String())
			req.Host = targetURL.Host
			proxy.ServeHTTP(w, req)
		}

		var currentHandler http.Handler = http.HandlerFunc(proxyHandler)

		currentHandler = middleware.AuthMiddleware(currentHandler, route.AuthRequired)

		r.Handle(route.Path, currentHandler).Methods(route.Method)
		log.Printf("Registered route: %s %s -> %s", route.Method, route.Path, route.TargetUrl)
	}

	r.HandleFunc("/login", handlers.LoginHandler).Methods("POST")

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Route not found: %s %s", r.Method, r.URL.Path)
		http.Error(w, "Not Found", http.StatusNotFound)
	})

	r.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Method not allowed: %s %s", r.Method, r.URL.Path)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
}

// getProxyRoutes reads the gateway.yaml configuration file.
func getProxyRoutes() ProxyRoute {
	f, err := os.ReadFile("configs/gateway.yaml")
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	var pr ProxyRoute
	if err := yaml.Unmarshal(f, &pr); err != nil {
		log.Fatalf("Error unmarshaling config: %v", err)
	}

	return pr
}
