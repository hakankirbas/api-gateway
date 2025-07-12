package services

import (
	"api-gateway/internal/k8s"
	"api-gateway/internal/middleware"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// DynamicRouteManager manages dynamic routing with real-time updates
type DynamicRouteManager struct {
	router           *mux.Router
	discoveryManager *DiscoveryManager
	authMiddleware   *middleware.AuthMiddleware

	// Route storage
	dynamicRoutes map[string]*DynamicRouteInfo
	routesMutex   sync.RWMutex

	// Enhanced load balancing and circuit breaking
	loadBalancerManager   *LoadBalancerManager
	circuitBreakerManager *middleware.CircuitBreakerManager

	// Statistics
	stats      *RouteStats
	statsMutex sync.RWMutex
}

// DynamicRouteInfo holds information about a dynamic route
type DynamicRouteInfo struct {
	ID            string                 `json:"id"`
	Path          string                 `json:"path"`
	Method        string                 `json:"method"`
	ServiceName   string                 `json:"service_name"`
	Namespace     string                 `json:"namespace"`
	AuthRequired  bool                   `json:"auth_required"`
	LoadBalancing string                 `json:"load_balancing"`
	Service       *k8s.DiscoveredService `json:"service"`
	CreatedAt     time.Time              `json:"created_at"`
	LastUsed      time.Time              `json:"last_used"`
	RequestCount  int64                  `json:"request_count"`
}

// RouteStats holds routing statistics
type RouteStats struct {
	TotalRoutes     int64            `json:"total_routes"`
	TotalRequests   int64            `json:"total_requests"`
	SuccessRequests int64            `json:"success_requests"`
	ErrorRequests   int64            `json:"error_requests"`
	AvgResponseTime time.Duration    `json:"avg_response_time"`
	RouteStats      map[string]int64 `json:"route_stats"`
}

// NewDynamicRouteManager creates a new enhanced dynamic route manager
func NewDynamicRouteManager(router *mux.Router, discoveryManager *DiscoveryManager, authMiddleware *middleware.AuthMiddleware) *DynamicRouteManager {
	// Circuit breaker configuration
	cbConfig := middleware.CircuitBreakerConfig{
		MaxRequests: 5,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts middleware.Counts) bool {
			// Trip if we have more than 5 consecutive failures or error rate > 50%
			return counts.ConsecutiveFailures > 5 ||
				(counts.Requests > 10 && counts.ErrorRate() > 0.5)
		},
		OnStateChange: func(name string, from middleware.CircuitBreakerState, to middleware.CircuitBreakerState) {
			log.Printf("Circuit breaker for service '%s' changed from %s to %s", name, from, to)
		},
		IsSuccessful: func(err error) bool {
			// Consider network errors as failures, but not circuit breaker errors
			if err == nil {
				return true
			}
			return !isNetworkError(err)
		},
	}

	drm := &DynamicRouteManager{
		router:                router,
		discoveryManager:      discoveryManager,
		authMiddleware:        authMiddleware,
		dynamicRoutes:         make(map[string]*DynamicRouteInfo),
		loadBalancerManager:   NewLoadBalancerManager(),
		circuitBreakerManager: middleware.NewCircuitBreakerManager(cbConfig),
		stats: &RouteStats{
			RouteStats: make(map[string]int64),
		},
	}

	discoveryManager.AddEventProcessor(drm)
	drm.registerDynamicHandler()

	return drm
}

// registerDynamicHandler registers the catch-all dynamic route handler
func (drm *DynamicRouteManager) registerDynamicHandler() {
	drm.router.PathPrefix("/").HandlerFunc(drm.handleDynamicRoute)
	log.Println("Enhanced dynamic route handler registered with PathPrefix")
}

// handleDynamicRoute handles all dynamic routes with enhanced load balancing and circuit breaking
func (drm *DynamicRouteManager) handleDynamicRoute(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	route := drm.findMatchingRoute(r.Method, r.URL.Path)
	if route == nil {
		log.Printf("No dynamic route found for %s %s", r.Method, r.URL.Path)
		return
	}

	log.Printf("Dynamic route matched: %s %s -> service: %s", r.Method, r.URL.Path, route.ServiceName)

	drm.updateRouteStats(route, startTime)

	// Enhanced endpoint selection with load balancing and circuit breaking
	endpoint := drm.selectHealthyEndpointEnhanced(route.ServiceName, route.Service.Endpoints)
	if endpoint.IP == "" {
		log.Printf("No healthy endpoint available for service: %s", route.ServiceName)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		drm.incrementErrorStats()
		return
	}

	log.Printf("Selected endpoint: %s:%d for service: %s", endpoint.IP, endpoint.Port, route.ServiceName)

	if route.AuthRequired {
		if !drm.checkAuthentication(w, r) {
			log.Printf("Authentication failed for %s %s", r.Method, r.URL.Path)
			drm.incrementErrorStats()
			return
		}
	}

	if err := drm.proxyRequestEnhanced(w, r, route, endpoint); err != nil {
		log.Printf("Proxy error for route %s %s: %v", route.Method, route.Path, err)
		if !strings.Contains(err.Error(), "circuit breaker") {
			http.Error(w, "Service Temporarily Unavailable", http.StatusServiceUnavailable)
		}
		drm.incrementErrorStats()
		return
	}

	drm.incrementSuccessStats()
	log.Printf("Successfully proxied %s %s to %s:%d", r.Method, r.URL.Path, endpoint.IP, endpoint.Port)
}

// selectHealthyEndpointEnhanced uses load balancing and circuit breaking
func (drm *DynamicRouteManager) selectHealthyEndpointEnhanced(serviceName string, endpoints []k8s.ServiceEndpoint) k8s.ServiceEndpoint {
	// Get or create load balancer for this service with configured strategy
	strategy := "round-robin" // Default, could be read from service annotations

	lb := drm.loadBalancerManager.GetOrCreateLoadBalancer(serviceName, strategy)

	// Update endpoints in load balancer
	lb.UpdateEndpoints(endpoints)

	// Get circuit breaker for this service
	cb := drm.circuitBreakerManager.GetCircuitBreaker(serviceName)

	// Try to get an endpoint through circuit breaker
	result, err := cb.Execute(func() (interface{}, error) {
		endpoint := lb.SelectEndpoint()
		if endpoint.IP == "" {
			return nil, errors.New("no healthy endpoints available")
		}
		return endpoint, nil
	})

	if err != nil {
		log.Printf("Circuit breaker blocked request to service %s: %v", serviceName, err)
		return k8s.ServiceEndpoint{}
	}

	return result.(k8s.ServiceEndpoint)
}

// proxyRequestEnhanced handles request proxying with circuit breaker protection
func (drm *DynamicRouteManager) proxyRequestEnhanced(w http.ResponseWriter, r *http.Request, route *DynamicRouteInfo, endpoint k8s.ServiceEndpoint) error {
	startTime := time.Now()

	// Get circuit breaker for this service
	cb := drm.circuitBreakerManager.GetCircuitBreaker(route.ServiceName)

	// Execute request through circuit breaker
	_, err := cb.Execute(func() (interface{}, error) {
		targetURL := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", endpoint.IP, endpoint.Port),
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		// Enhanced proxy director with better error handling
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.URL.Host = targetURL.Host
			req.URL.Scheme = targetURL.Scheme
			req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
			req.Header.Set("X-Gateway-Service", route.ServiceName)
			req.Header.Set("X-Gateway-Endpoint", endpoint.IP)
			req.Header.Set("X-Request-Start", startTime.Format(time.RFC3339Nano))
			req.Host = targetURL.Host
		}

		// Enhanced error handler
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			duration := time.Since(startTime)
			log.Printf("Proxy error for service %s (endpoint %s:%d) after %v: %v",
				route.ServiceName, endpoint.IP, endpoint.Port, duration, err)

			// Return error to circuit breaker for evaluation
			return
		}

		// Execute proxy
		proxy.ServeHTTP(w, r)
		return nil, nil
	})

	return err
}

// ProcessServiceEvent implements EventProcessor interface
func (drm *DynamicRouteManager) ProcessServiceEvent(event k8s.ServiceEvent) error {
	switch event.Type {
	case k8s.ServiceAdded:
		return drm.addRoute(event.Service)
	case k8s.ServiceModified:
		return drm.updateRoute(event.Service)
	case k8s.ServiceDeleted:
		return drm.removeRoute(event.Service)
	}
	return nil
}

// addRoute adds a new dynamic route
func (drm *DynamicRouteManager) addRoute(service *k8s.DiscoveredService) error {
	drm.routesMutex.Lock()
	defer drm.routesMutex.Unlock()

	routeKey := fmt.Sprintf("%s:%s", service.Method, service.Path)

	route := &DynamicRouteInfo{
		ID:            routeKey,
		Path:          service.Path,
		Method:        service.Method,
		ServiceName:   service.Name,
		Namespace:     service.Namespace,
		AuthRequired:  service.AuthRequired,
		LoadBalancing: service.LoadBalancing,
		Service:       service,
		CreatedAt:     time.Now(),
		LastUsed:      time.Now(),
	}

	drm.dynamicRoutes[routeKey] = route

	// Update load balancer with new endpoints
	drm.loadBalancerManager.UpdateServiceEndpoints(service.Name, service.Endpoints)

	drm.statsMutex.Lock()
	drm.stats.TotalRoutes++
	drm.statsMutex.Unlock()

	log.Printf("Dynamic route added: %s %s -> %s.%s (auth: %v, lb: %s)",
		route.Method, route.Path, route.ServiceName, route.Namespace,
		route.AuthRequired, route.LoadBalancing)

	return nil
}

// updateRoute updates an existing dynamic route
func (drm *DynamicRouteManager) updateRoute(service *k8s.DiscoveredService) error {
	routeKey := fmt.Sprintf("%s:%s", service.Method, service.Path)

	drm.routesMutex.Lock()
	_, exists := drm.dynamicRoutes[routeKey]
	drm.routesMutex.Unlock()

	if !exists {
		return drm.addRoute(service)
	}

	// Update load balancer with new endpoints
	drm.loadBalancerManager.UpdateServiceEndpoints(service.Name, service.Endpoints)

	drm.routesMutex.Lock()
	if route, exists := drm.dynamicRoutes[routeKey]; exists {
		route.Service = service
		route.LastUsed = time.Now()
		route.LoadBalancing = service.LoadBalancing
	}
	drm.routesMutex.Unlock()

	log.Printf("Dynamic route updated: %s %s -> %s.%s (lb: %s)",
		service.Method, service.Path, service.Name, service.Namespace, service.LoadBalancing)

	return nil
}

// removeRoute removes a dynamic route
func (drm *DynamicRouteManager) removeRoute(service *k8s.DiscoveredService) error {
	drm.routesMutex.Lock()
	defer drm.routesMutex.Unlock()

	routeKey := fmt.Sprintf("%s:%s", service.Method, service.Path)

	if _, exists := drm.dynamicRoutes[routeKey]; exists {
		delete(drm.dynamicRoutes, routeKey)

		drm.statsMutex.Lock()
		drm.stats.TotalRoutes--
		drm.statsMutex.Unlock()

		log.Printf("Dynamic route removed: %s %s", service.Method, service.Path)
	}

	return nil
}

// findMatchingRoute finds a matching route for the given method and path
func (drm *DynamicRouteManager) findMatchingRoute(method, path string) *DynamicRouteInfo {
	drm.routesMutex.RLock()
	defer drm.routesMutex.RUnlock()

	routeKey := fmt.Sprintf("%s:%s", method, path)

	if route, exists := drm.dynamicRoutes[routeKey]; exists {
		log.Printf("Exact route match found: %s -> %s", routeKey, route.ServiceName)
		return route
	}

	log.Printf("No route found for: %s", routeKey)
	log.Printf("Available routes: %v", drm.getRouteKeys())
	return nil
}

// Helper methods
func (drm *DynamicRouteManager) getRouteKeys() []string {
	var keys []string
	for k := range drm.dynamicRoutes {
		keys = append(keys, k)
	}
	return keys
}

func (drm *DynamicRouteManager) checkAuthentication(w http.ResponseWriter, r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "Authorization header required", http.StatusUnauthorized)
		return false
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Invalid token format", http.StatusUnauthorized)
		return false
	}

	return true
}

func (drm *DynamicRouteManager) updateRouteStats(route *DynamicRouteInfo, startTime time.Time) {
	drm.routesMutex.Lock()
	route.LastUsed = time.Now()
	route.RequestCount++
	drm.routesMutex.Unlock()

	drm.statsMutex.Lock()
	drm.stats.TotalRequests++
	drm.stats.RouteStats[route.ID]++
	drm.statsMutex.Unlock()
}

func (drm *DynamicRouteManager) incrementSuccessStats() {
	drm.statsMutex.Lock()
	drm.stats.SuccessRequests++
	drm.statsMutex.Unlock()
}

func (drm *DynamicRouteManager) incrementErrorStats() {
	drm.statsMutex.Lock()
	drm.stats.ErrorRequests++
	drm.statsMutex.Unlock()
}

// Helper function to determine if error is network-related
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	networkErrors := []string{
		"connection refused",
		"no such host",
		"network is unreachable",
		"timeout",
		"connection reset",
		"broken pipe",
	}

	for _, netErr := range networkErrors {
		if strings.Contains(strings.ToLower(errStr), netErr) {
			return true
		}
	}
	return false
}

// GetRouteInfo returns information about all dynamic routes
func (drm *DynamicRouteManager) GetRouteInfo() map[string]*DynamicRouteInfo {
	drm.routesMutex.RLock()
	defer drm.routesMutex.RUnlock()

	routes := make(map[string]*DynamicRouteInfo)
	for k, v := range drm.dynamicRoutes {
		routes[k] = v
	}
	return routes
}

// GetStats returns routing statistics
func (drm *DynamicRouteManager) GetStats() *RouteStats {
	drm.statsMutex.RLock()
	defer drm.statsMutex.RUnlock()

	stats := &RouteStats{
		TotalRoutes:     drm.stats.TotalRoutes,
		TotalRequests:   drm.stats.TotalRequests,
		SuccessRequests: drm.stats.SuccessRequests,
		ErrorRequests:   drm.stats.ErrorRequests,
		AvgResponseTime: drm.stats.AvgResponseTime,
		RouteStats:      make(map[string]int64),
	}

	for k, v := range drm.stats.RouteStats {
		stats.RouteStats[k] = v
	}

	return stats
}

// Enhanced admin endpoints
func (drm *DynamicRouteManager) SetupAdminEndpoints(router *mux.Router) {
	// Load balancer statistics endpoint
	router.HandleFunc("/admin/load-balancers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats := drm.loadBalancerManager.GetAllStats()
		json.NewEncoder(w).Encode(stats)
	}).Methods("GET")

	// Circuit breaker statistics endpoint
	router.HandleFunc("/admin/circuit-breakers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats := drm.circuitBreakerManager.GetStats()
		json.NewEncoder(w).Encode(stats)
	}).Methods("GET")

	// Service health overview endpoint
	router.HandleFunc("/admin/health-overview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		overview := struct {
			Services        map[string]*DynamicRouteInfo              `json:"services"`
			LoadBalancers   map[string]LoadBalancerStats              `json:"load_balancers"`
			CircuitBreakers map[string]middleware.CircuitBreakerStats `json:"circuit_breakers"`
			Summary         map[string]interface{}                    `json:"summary"`
		}{
			Services:        drm.GetRouteInfo(),
			LoadBalancers:   drm.loadBalancerManager.GetAllStats(),
			CircuitBreakers: drm.circuitBreakerManager.GetStats(),
		}

		// Calculate summary statistics
		totalServices := len(overview.Services)
		healthyServices := 0
		openCircuits := 0

		for _, service := range overview.Services {
			hasHealthyEndpoints := false
			for _, endpoint := range service.Service.Endpoints {
				if endpoint.Ready {
					hasHealthyEndpoints = true
					break
				}
			}
			if hasHealthyEndpoints {
				healthyServices++
			}
		}

		for _, cb := range overview.CircuitBreakers {
			if cb.State == middleware.StateOpen {
				openCircuits++
			}
		}

		overview.Summary = map[string]interface{}{
			"total_services":     totalServices,
			"healthy_services":   healthyServices,
			"unhealthy_services": totalServices - healthyServices,
			"open_circuits":      openCircuits,
			"service_health_rate": func() float64 {
				if totalServices == 0 {
					return 100.0
				}
				return float64(healthyServices) / float64(totalServices) * 100
			}(),
		}

		json.NewEncoder(w).Encode(overview)
	}).Methods("GET")
}
