package router

import (
	"api-gateway/internal/config"
	"api-gateway/internal/handlers"
	"api-gateway/internal/middleware"
	"api-gateway/internal/services"
	"api-gateway/pkg/jwt"
	"context"
	"encoding/json"
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

// ProxyRoute represents the structure of our gateway.yaml (legacy)
type ProxyRoute struct {
	Routes []struct {
		Path         string `yaml:"path"`
		Method       string `yaml:"method"`
		TargetUrl    string `yaml:"target_url"`
		AuthRequired bool   `yaml:"auth_required"`
	} `yaml:"routes"`
}

// HealthManager manages the health status of backend services (legacy)
type HealthManager struct {
	statuses      map[string]bool
	mu            sync.RWMutex
	client        *http.Client
	checkInterval time.Duration
	stopCh        chan struct{}
}

// Setup initializes and starts the API Gateway server with service discovery
func Setup(cfg *config.Config) {
	ctx := context.Background()

	discoveryManager := services.NewDiscoveryManager(cfg)

	if err := discoveryManager.Start(ctx); err != nil {
		log.Fatalf("Failed to start discovery manager: %v", err)
	}

	jwtService := jwt.NewService(cfg.JWT)
	authMiddleware := middleware.NewAuthMiddleware(jwtService)

	r := mux.NewRouter()

	rateLimiter := middleware.NewRateLimiter(
		rate.Limit(cfg.Rate.Limit),
		cfg.Rate.BurstLimit,
		cfg.Rate.CleanupInterval,
	)
	r.Use(rateLimiter.Middleware)
	r.Use(middleware.LoggingMiddleware)

	setupRoutes(r, cfg, authMiddleware, jwtService, discoveryManager)

	dynamicRouteManager := services.NewDynamicRouteManager(r, discoveryManager, authMiddleware)
	_ = dynamicRouteManager

	server := &http.Server{
		Addr:         cfg.Server.Port,
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	log.Printf("API Gateway started on %s", cfg.Server.Port)
	log.Printf("Kubernetes integration: %v", cfg.Kubernetes.Enabled)
	log.Printf("Service discovery: %v", cfg.Kubernetes.ServiceDiscovery)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error while initializing API Gateway: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutdown signal received. API Gateway is shutting down...")

	discoveryManager.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("API Gateway failed to shut down properly: %v", err)
	}

	log.Println("API Gateway was closed successfully.")
}

// setupRoutes configures both static and dynamic routes
func setupRoutes(r *mux.Router, cfg *config.Config, authMiddleware *middleware.AuthMiddleware, jwtService *jwt.Service, discoveryManager *services.DiscoveryManager) {
	setupCoreRoutes(r, jwtService)

	setupDiscoveryRoutes(r, discoveryManager)

	if !cfg.Kubernetes.ServiceDiscovery {
		log.Println("Service discovery disabled, using static route configuration")
		setupStaticRoutes(r, cfg, authMiddleware)
	} else {
		log.Println("Service discovery enabled, routes will be managed dynamically")
	}

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Route not found: %s %s", r.Method, r.URL.Path)
		http.Error(w, "Not Found", http.StatusNotFound)
	})

	r.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Method not allowed: %s %s", r.Method, r.URL.Path)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
}

// setupCoreRoutes sets up core API endpoints
func setupCoreRoutes(r *mux.Router, jwtService *jwt.Service) {
	loginHandler := handlers.NewLoginHandler(jwtService)
	r.HandleFunc("/login", loginHandler.Handle).Methods("POST")

	r.HandleFunc("/health", handlers.HealthHandler).Methods("GET")
	r.HandleFunc("/ready", handlers.ReadinessHandler).Methods("GET")
	r.HandleFunc("/metrics", handlers.MetricsHandler).Methods("GET")

	log.Println("Core routes registered: /login, /health, /ready, /metrics")
}

// setupDiscoveryRoutes sets up service discovery and admin endpoints
func setupDiscoveryRoutes(r *mux.Router, discoveryManager *services.DiscoveryManager) {
	r.HandleFunc("/admin/services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		services := discoveryManager.GetDiscoveredServices()

		response := make(map[string]interface{})
		for name, service := range services {
			response[name] = map[string]interface{}{
				"name":           service.Name,
				"namespace":      service.Namespace,
				"path":           service.Path,
				"method":         service.Method,
				"auth_required":  service.AuthRequired,
				"load_balancing": service.LoadBalancing,
				"endpoints":      service.Endpoints,
				"last_updated":   service.LastUpdated,
			}
		}

		if err := writeJSONResponse(w, response); err != nil {
			log.Printf("Error writing services response: %v", err)
		}
	}).Methods("GET")

	r.HandleFunc("/admin/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		routes := discoveryManager.GetRoutes()

		response := make(map[string]interface{})
		for key, route := range routes {
			response[key] = map[string]interface{}{
				"path":          route.Path,
				"method":        route.Method,
				"service_name":  route.ServiceName,
				"namespace":     route.Namespace,
				"auth_required": route.AuthRequired,
				"endpoints":     len(route.Endpoints),
				"last_updated":  route.LastUpdated,
			}
		}

		if err := writeJSONResponse(w, response); err != nil {
			log.Printf("Error writing routes response: %v", err)
		}
	}).Methods("GET")

	r.HandleFunc("/admin/discovery/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats := discoveryManager.GetStats()

		if err := writeJSONResponse(w, stats); err != nil {
			log.Printf("Error writing stats response: %v", err)
		}
	}).Methods("GET")

	log.Println("Discovery routes registered: /admin/services, /admin/routes, /admin/discovery/stats")
}

// setupStaticRoutes sets up legacy static routes from gateway.yaml
func setupStaticRoutes(r *mux.Router, cfg *config.Config, authMiddleware *middleware.AuthMiddleware) {
	pr := getProxyRoutes()

	healthManager := NewHealthManager(cfg.Health.CheckInterval, cfg.Health.Timeout)
	healthManager.StartHealthChecks(pr.Routes)

	pr.registerProxies(r, healthManager, authMiddleware)

	log.Printf("Static routes registered: %d routes from gateway.yaml", len(pr.Routes))
}

// Legacy functions for static route support
func NewHealthManager(interval, timeout time.Duration) *HealthManager {
	return &HealthManager{
		statuses:      make(map[string]bool),
		client:        &http.Client{Timeout: timeout},
		checkInterval: interval,
		stopCh:        make(chan struct{}),
	}
}

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

func (hm *HealthManager) checkTargetHealth(targetURL string) {
	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hm.performCheck(targetURL)
		case <-hm.stopCh:
			return
		}
	}
}

func (hm *HealthManager) performCheck(targetURL string) {
	healthCheckURL := targetURL + "/health"
	resp, err := hm.client.Get(healthCheckURL)
	isHealthy := false
	if resp != nil {
		isHealthy = err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400
		resp.Body.Close()
	}

	hm.mu.Lock()
	hm.statuses[targetURL] = isHealthy
	hm.mu.Unlock()
}

func (hm *HealthManager) IsHealthy(targetURL string) bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.statuses[targetURL]
}

func (hm *HealthManager) StopHealthChecks() {
	close(hm.stopCh)
}

func (pr *ProxyRoute) registerProxies(r *mux.Router, hm *HealthManager, authMiddleware *middleware.AuthMiddleware) {
	for _, route := range pr.Routes {
		targetURL, err := url.Parse(route.TargetUrl)
		if err != nil {
			log.Printf("Error parsing target URL %s: %v", route.TargetUrl, err)
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		proxyHandler := func(w http.ResponseWriter, req *http.Request) {
			if !hm.IsHealthy(targetURL.String()) {
				log.Printf("Service %s is unhealthy. Returning 503", targetURL.String())
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
				return
			}

			log.Printf("Proxying request: %s %s to %s", req.Method, req.URL.Path, targetURL.String())
			req.Host = targetURL.Host
			proxy.ServeHTTP(w, req)
		}

		var currentHandler http.Handler = http.HandlerFunc(proxyHandler)
		currentHandler = authMiddleware.Middleware(route.AuthRequired)(currentHandler)

		r.Handle(route.Path, currentHandler).Methods(route.Method)
		log.Printf("Static route registered: %s %s -> %s", route.Method, route.Path, route.TargetUrl)
	}
}

func getProxyRoutes() ProxyRoute {
	f, err := os.ReadFile("configs/gateway.yaml")
	if err != nil {
		log.Printf("Warning: Could not read gateway.yaml: %v", err)
		return ProxyRoute{Routes: []struct {
			Path         string `yaml:"path"`
			Method       string `yaml:"method"`
			TargetUrl    string `yaml:"target_url"`
			AuthRequired bool   `yaml:"auth_required"`
		}{}}
	}

	var pr ProxyRoute
	if err := yaml.Unmarshal(f, &pr); err != nil {
		log.Printf("Warning: Could not parse gateway.yaml: %v", err)
		return ProxyRoute{Routes: []struct {
			Path         string `yaml:"path"`
			Method       string `yaml:"method"`
			TargetUrl    string `yaml:"target_url"`
			AuthRequired bool   `yaml:"auth_required"`
		}{}}
	}

	return pr
}

// Helper function to write JSON responses
func writeJSONResponse(w http.ResponseWriter, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	return encoder.Encode(data)
}
