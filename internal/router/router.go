package router

import (
	"api-gateway/internal/config"
	"api-gateway/internal/handlers"
	"api-gateway/internal/middleware"
	"api-gateway/internal/services"
	"api-gateway/pkg/jwt"
	"api-gateway/pkg/logger"
	"context"
	"encoding/json"
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
	logger        *logger.Logger
}

// Setup initializes and starts the API Gateway server with structured logging
func Setup(cfg *config.Config) {
	ctx := context.Background()

	structuredLogger := logger.NewLogger(logger.Config{
		Level:       cfg.Logging.Level,
		Format:      "json",
		Service:     "api-gateway",
		Output:      "stdout",
		EnableHooks: false,
	})

	// Add custom hooks if webhook URLs are configured
	if cfg.Logging.SlackWebhookURL != "" {
		slackHook := logger.NewSlackHook(cfg.Logging.SlackWebhookURL)
		structuredLogger.AddHook(slackHook)
	}

	if cfg.Logging.LokiURL != "" {
		lokiHook := logger.NewLokiHook(cfg.Logging.LokiURL)
		structuredLogger.AddHook(lokiHook)
	}

	// Add metrics hook
	metricsHook := logger.NewMetricsHook()
	structuredLogger.AddHook(metricsHook)

	appLogger := structuredLogger.WithComponent("startup")
	appLogger.Info("API Gateway starting", map[string]interface{}{
		"version":      "1.0.0",
		"environment":  os.Getenv("ENVIRONMENT"),
		"log_format":   "json",
		"log_level":    cfg.Logging.Level,
		"startup_time": time.Now().UTC(),
	})

	// Initialize discovery manager
	discoveryManager := services.NewDiscoveryManager(cfg)
	discoveryLogger := structuredLogger.WithComponent("discovery")

	if err := discoveryManager.Start(ctx); err != nil {
		appLogger.Fatal("Failed to start discovery manager", map[string]interface{}{
			"error": err,
		})
	}

	discoveryLogger.Info("Discovery manager started successfully")

	// Initialize JWT service
	jwtService := jwt.NewService(cfg.JWT)
	authMiddleware := middleware.NewAuthMiddleware(jwtService)

	// Create router
	r := mux.NewRouter()

	// Apply middlewares in order
	r.Use(middleware.NewRequestIDMiddleware().Middleware)
	r.Use(middleware.NewPanicRecoveryMiddleware(structuredLogger).Middleware)
	r.Use(middleware.NewStructuredLoggingMiddleware(structuredLogger).Middleware)

	// Rate limiting
	rateLimiter := middleware.NewRateLimiter(
		rate.Limit(cfg.Rate.Limit),
		cfg.Rate.BurstLimit,
		cfg.Rate.CleanupInterval,
	)
	r.Use(rateLimiter.Middleware)

	// Setup routes
	setupRoutes(r, cfg, authMiddleware, jwtService, discoveryManager, structuredLogger)

	// Initialize dynamic route manager
	dynamicRouteManager := services.NewDynamicRouteManager(r, discoveryManager, authMiddleware)
	_ = dynamicRouteManager

	// Create HTTP server
	server := &http.Server{
		Addr:         cfg.Server.Port,
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	appLogger.Info("API Gateway configuration loaded", map[string]interface{}{
		"port":              cfg.Server.Port,
		"read_timeout":      cfg.Server.ReadTimeout,
		"write_timeout":     cfg.Server.WriteTimeout,
		"kubernetes":        cfg.Kubernetes.Enabled,
		"service_discovery": cfg.Kubernetes.ServiceDiscovery,
		"namespace":         cfg.Kubernetes.Namespace,
	})

	// Start server in goroutine
	go func() {
		appLogger.Info("Starting HTTP server", map[string]interface{}{
			"address": cfg.Server.Port,
		})

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			appLogger.Fatal("Failed to start HTTP server", map[string]interface{}{
				"error": err,
			})
		}
	}()

	appLogger.Info("API Gateway started successfully", map[string]interface{}{
		"port": cfg.Server.Port,
	})

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	appLogger.Info("Shutdown signal received", map[string]interface{}{
		"signal": sig.String(),
	})

	// Graceful shutdown
	discoveryManager.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		appLogger.Error("Failed to shutdown server gracefully", map[string]interface{}{
			"error": err,
		})
	} else {
		appLogger.Info("Server shutdown completed successfully")
	}
}

// setupRoutes configures both static and dynamic routes with logging
func setupRoutes(r *mux.Router, cfg *config.Config, authMiddleware *middleware.AuthMiddleware,
	jwtService *jwt.Service, discoveryManager *services.DiscoveryManager, structuredLogger *logger.Logger) {

	routerLogger := structuredLogger.WithComponent("router")

	setupCoreRoutes(r, jwtService, structuredLogger)
	setupDiscoveryRoutes(r, discoveryManager, structuredLogger)

	// Enhanced dynamic route manager
	var dynamicRouteManager *services.DynamicRouteManager

	if !cfg.Kubernetes.ServiceDiscovery {
		routerLogger.Info("Service discovery disabled, using static route configuration")
		setupStaticRoutes(r, cfg, authMiddleware, structuredLogger)
	} else {
		routerLogger.Info("Service discovery enabled, routes will be managed dynamically")

		// Create enhanced dynamic route manager
		dynamicRouteManager = services.NewDynamicRouteManager(r, discoveryManager, authMiddleware)

		// Setup admin endpoints for the enhanced features
		dynamicRouteManager.SetupAdminEndpoints(r)

		routerLogger.Info("Enhanced dynamic route manager initialized with load balancing and circuit breaking")
	}

	// Enhanced 404 handler with logging
	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextLogger := structuredLogger.WithContext(r.Context()).WithComponent("router")
		contextLogger.Warn("Route not found", map[string]interface{}{
			"method": r.Method,
			"path":   r.URL.Path,
		})
		http.Error(w, "Not Found", http.StatusNotFound)
	})

	// Enhanced 405 handler with logging
	r.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextLogger := structuredLogger.WithContext(r.Context()).WithComponent("router")
		contextLogger.Warn("Method not allowed", map[string]interface{}{
			"method": r.Method,
			"path":   r.URL.Path,
		})
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	routerLogger.Info("All routes configured successfully")
}

// setupCoreRoutes sets up core API endpoints with logging
func setupCoreRoutes(r *mux.Router, jwtService *jwt.Service, structuredLogger *logger.Logger) {
	coreLogger := structuredLogger.WithComponent("core_routes")

	loginHandler := handlers.NewLoginHandler(jwtService)
	r.HandleFunc("/login", loginHandler.Handle).Methods("POST")

	r.HandleFunc("/health", handlers.HealthHandler).Methods("GET")
	r.HandleFunc("/ready", handlers.ReadinessHandler).Methods("GET")
	r.HandleFunc("/metrics", handlers.MetricsHandler).Methods("GET")

	coreLogger.Info("Core routes registered", map[string]interface{}{
		"routes": []string{"/login", "/health", "/ready", "/metrics"},
	})
}

// setupDiscoveryRoutes sets up service discovery and admin endpoints with logging
func setupDiscoveryRoutes(r *mux.Router, discoveryManager *services.DiscoveryManager, structuredLogger *logger.Logger) {
	discoveryLogger := structuredLogger.WithComponent("discovery_routes")

	r.HandleFunc("/admin/services", func(w http.ResponseWriter, r *http.Request) {
		contextLogger := structuredLogger.WithContext(r.Context()).WithComponent("admin")

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

		contextLogger.Info("Admin services endpoint accessed", map[string]interface{}{
			"service_count": len(services),
		})

		if err := json.NewEncoder(w).Encode(response); err != nil {
			contextLogger.Error("Failed to write services response", map[string]interface{}{
				"error": err,
			})
		}
	}).Methods("GET")

	r.HandleFunc("/admin/routes", func(w http.ResponseWriter, r *http.Request) {
		contextLogger := structuredLogger.WithContext(r.Context()).WithComponent("admin")

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

		contextLogger.Info("Admin routes endpoint accessed", map[string]interface{}{
			"route_count": len(routes),
		})

		if err := json.NewEncoder(w).Encode(response); err != nil {
			contextLogger.Error("Failed to write routes response", map[string]interface{}{
				"error": err,
			})
		}
	}).Methods("GET")

	r.HandleFunc("/admin/discovery/stats", func(w http.ResponseWriter, r *http.Request) {
		contextLogger := structuredLogger.WithContext(r.Context()).WithComponent("admin")

		w.Header().Set("Content-Type", "application/json")
		stats := discoveryManager.GetStats()

		contextLogger.Info("Admin stats endpoint accessed")

		if err := json.NewEncoder(w).Encode(stats); err != nil {
			contextLogger.Error("Failed to write stats response", map[string]interface{}{
				"error": err,
			})
		}
	}).Methods("GET")

	discoveryLogger.Info("Discovery admin routes registered", map[string]interface{}{
		"routes": []string{"/admin/services", "/admin/routes", "/admin/discovery/stats"},
	})
}

// setupStaticRoutes sets up legacy static routes from gateway.yaml with logging
func setupStaticRoutes(r *mux.Router, cfg *config.Config, authMiddleware *middleware.AuthMiddleware, structuredLogger *logger.Logger) {
	staticLogger := structuredLogger.WithComponent("static_routes")

	pr := getProxyRoutes(structuredLogger)

	healthManager := NewHealthManager(cfg.Health.CheckInterval, cfg.Health.Timeout, structuredLogger)
	healthManager.StartHealthChecks(pr.Routes)

	pr.registerProxies(r, healthManager, authMiddleware, structuredLogger)

	staticLogger.Info("Static routes configuration completed", map[string]interface{}{
		"route_count": len(pr.Routes),
	})
}

// NewHealthManager creates a health manager with logging
func NewHealthManager(interval, timeout time.Duration, structuredLogger *logger.Logger) *HealthManager {
	return &HealthManager{
		statuses:      make(map[string]bool),
		client:        &http.Client{Timeout: timeout},
		checkInterval: interval,
		stopCh:        make(chan struct{}),
		logger:        structuredLogger.WithComponent("health_manager"),
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

	hm.logger.Info("Starting health checks", map[string]interface{}{
		"target_count": len(uniqueTargets),
		"interval":     hm.checkInterval,
	})

	for targetURL := range uniqueTargets {
		go hm.checkTargetHealth(targetURL)
	}
}

func (hm *HealthManager) checkTargetHealth(targetURL string) {
	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	hm.logger.Debug("Health check started for target", map[string]interface{}{
		"target_url": targetURL,
	})

	for {
		select {
		case <-ticker.C:
			hm.performCheck(targetURL)
		case <-hm.stopCh:
			hm.logger.Debug("Health check stopped for target", map[string]interface{}{
				"target_url": targetURL,
			})
			return
		}
	}
}

func (hm *HealthManager) performCheck(targetURL string) {
	healthCheckURL := targetURL + "/health"

	start := time.Now()
	resp, err := hm.client.Get(healthCheckURL)
	duration := time.Since(start)

	isHealthy := false
	statusCode := 0

	if resp != nil {
		statusCode = resp.StatusCode
		isHealthy = err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400
		resp.Body.Close()
	}

	// Update status
	hm.mu.Lock()
	previousStatus := hm.statuses[targetURL]
	hm.statuses[targetURL] = isHealthy
	hm.mu.Unlock()

	// Log health check result
	fields := map[string]interface{}{
		"target_url":  targetURL,
		"healthy":     isHealthy,
		"status_code": statusCode,
		"duration":    duration,
		"check_url":   healthCheckURL,
	}

	if err != nil {
		fields["error"] = err
	}

	// Log status changes or errors
	if !isHealthy {
		if previousStatus {
			hm.logger.Warn("Service became unhealthy", fields)
		} else {
			hm.logger.Debug("Service health check failed", fields)
		}
	} else if !previousStatus && isHealthy {
		hm.logger.Info("Service became healthy", fields)
	} else {
		hm.logger.Debug("Service health check successful", fields)
	}
}

func (hm *HealthManager) IsHealthy(targetURL string) bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.statuses[targetURL]
}

func (hm *HealthManager) StopHealthChecks() {
	hm.logger.Info("Stopping all health checks")
	close(hm.stopCh)
}

func (pr *ProxyRoute) registerProxies(r *mux.Router, hm *HealthManager, authMiddleware *middleware.AuthMiddleware, structuredLogger *logger.Logger) {
	proxyLogger := structuredLogger.WithComponent("proxy")

	for _, route := range pr.Routes {
		targetURL, err := url.Parse(route.TargetUrl)
		if err != nil {
			proxyLogger.Error("Invalid target URL", map[string]interface{}{
				"target_url": route.TargetUrl,
				"error":      err,
			})
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		// Enhanced proxy handler with detailed logging
		proxyHandler := func(w http.ResponseWriter, req *http.Request) {
			contextLogger := structuredLogger.WithContext(req.Context()).WithComponent("proxy")

			if !hm.IsHealthy(targetURL.String()) {
				contextLogger.Warn("Service unavailable - health check failed", map[string]interface{}{
					"target_url": targetURL.String(),
					"method":     req.Method,
					"path":       req.URL.Path,
				})
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
				return
			}

			start := time.Now()

			contextLogger.Info("Proxying request to backend", map[string]interface{}{
				"method":     req.Method,
				"path":       req.URL.Path,
				"target_url": targetURL.String(),
			})

			// Set original host for backend
			req.Host = targetURL.Host

			// Custom error handler for proxy
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				duration := time.Since(start)
				contextLogger.Error("Proxy request failed", map[string]interface{}{
					"error":      err,
					"method":     r.Method,
					"path":       r.URL.Path,
					"target_url": targetURL.String(),
					"duration":   duration,
				})
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
			}

			// Execute proxy
			proxy.ServeHTTP(w, req)

			duration := time.Since(start)
			contextLogger.Info("Proxy request completed", map[string]interface{}{
				"method":     req.Method,
				"path":       req.URL.Path,
				"target_url": targetURL.String(),
				"duration":   duration,
			})
		}

		var currentHandler http.Handler = http.HandlerFunc(proxyHandler)
		currentHandler = authMiddleware.Middleware(route.AuthRequired)(currentHandler)

		r.Handle(route.Path, currentHandler).Methods(route.Method)

		proxyLogger.Info("Static route registered", map[string]interface{}{
			"method":        route.Method,
			"path":          route.Path,
			"target_url":    route.TargetUrl,
			"auth_required": route.AuthRequired,
		})
	}
}

func getProxyRoutes(structuredLogger *logger.Logger) ProxyRoute {
	configLogger := structuredLogger.WithComponent("config")

	f, err := os.ReadFile("configs/gateway.yaml")
	if err != nil {
		configLogger.Warn("Could not read gateway.yaml, using empty configuration", map[string]interface{}{
			"error": err,
		})
		return ProxyRoute{Routes: []struct {
			Path         string `yaml:"path"`
			Method       string `yaml:"method"`
			TargetUrl    string `yaml:"target_url"`
			AuthRequired bool   `yaml:"auth_required"`
		}{}}
	}

	var pr ProxyRoute
	if err := yaml.Unmarshal(f, &pr); err != nil {
		configLogger.Error("Could not parse gateway.yaml", map[string]interface{}{
			"error": err,
		})
		return ProxyRoute{Routes: []struct {
			Path         string `yaml:"path"`
			Method       string `yaml:"method"`
			TargetUrl    string `yaml:"target_url"`
			AuthRequired bool   `yaml:"auth_required"`
		}{}}
	}

	configLogger.Info("Gateway configuration loaded", map[string]interface{}{
		"route_count": len(pr.Routes),
	})

	return pr
}

// Helper function to write JSON responses with error logging
func writeJSONResponse(w http.ResponseWriter, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	return encoder.Encode(data)
}
