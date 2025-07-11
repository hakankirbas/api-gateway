package services

import (
	"api-gateway/internal/k8s"
	"api-gateway/internal/middleware"
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

	// Load balancing
	loadBalancers map[string]*LoadBalancer
	lbMutex       sync.RWMutex

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

// LoadBalancer handles load balancing for a service
type LoadBalancer struct {
	Strategy    string                `json:"strategy"`
	Endpoints   []k8s.ServiceEndpoint `json:"endpoints"`
	Current     int                   `json:"current"`
	mutex       sync.Mutex
	LastUpdated time.Time `json:"last_updated"`
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

// NewDynamicRouteManager creates a new dynamic route manager
func NewDynamicRouteManager(router *mux.Router, discoveryManager *DiscoveryManager, authMiddleware *middleware.AuthMiddleware) *DynamicRouteManager {
	drm := &DynamicRouteManager{
		router:           router,
		discoveryManager: discoveryManager,
		authMiddleware:   authMiddleware,
		dynamicRoutes:    make(map[string]*DynamicRouteInfo),
		loadBalancers:    make(map[string]*LoadBalancer),
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
	log.Println("Dynamic route handler registered with PathPrefix")
}

// handleDynamicRoute handles all dynamic routes - COMPLETELY REWRITTEN
func (drm *DynamicRouteManager) handleDynamicRoute(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	route := drm.findMatchingRoute(r.Method, r.URL.Path)
	if route == nil {
		log.Printf("No dynamic route found for %s %s", r.Method, r.URL.Path)
		return
	}

	log.Printf("Dynamic route matched: %s %s -> service: %s", r.Method, r.URL.Path, route.ServiceName)

	drm.updateRouteStats(route, startTime)

	endpoints := drm.discoveryManager.GetServiceEndpoints(route.ServiceName)
	if len(endpoints) == 0 {
		log.Printf("No healthy endpoints available for service: %s", route.ServiceName)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		drm.incrementErrorStats()
		return
	}

	endpoint := drm.selectHealthyEndpoint(endpoints)
	if endpoint.IP == "" {
		log.Printf("No healthy endpoint selected for service: %s", route.ServiceName)
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

	if err := drm.proxyRequest(w, r, route, endpoint); err != nil {
		log.Printf("Proxy error for route %s %s: %v", route.Method, route.Path, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		drm.incrementErrorStats()
		return
	}

	drm.incrementSuccessStats()
	log.Printf("Successfully proxied %s %s to %s:%d", r.Method, r.URL.Path, endpoint.IP, endpoint.Port)
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

	drm.updateLoadBalancer(service)

	drm.statsMutex.Lock()
	drm.stats.TotalRoutes++
	drm.statsMutex.Unlock()

	log.Printf("Dynamic route added: %s %s -> %s.%s (auth: %v)",
		route.Method, route.Path, route.ServiceName, route.Namespace, route.AuthRequired)

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

	drm.updateLoadBalancer(service)

	drm.routesMutex.Lock()
	if route, exists := drm.dynamicRoutes[routeKey]; exists {
		route.Service = service
		route.LastUsed = time.Now()
	}
	drm.routesMutex.Unlock()

	log.Printf("Dynamic route updated: %s %s -> %s.%s",
		service.Method, service.Path, service.Name, service.Namespace)

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

// FIXED: Simplified and working route matching
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

// Helper to debug route keys
func (drm *DynamicRouteManager) getRouteKeys() []string {
	var keys []string
	for k := range drm.dynamicRoutes {
		keys = append(keys, k)
	}
	return keys
}

// FIXED: Simple endpoint selection
func (drm *DynamicRouteManager) selectHealthyEndpoint(endpoints []k8s.ServiceEndpoint) k8s.ServiceEndpoint {
	for _, endpoint := range endpoints {
		if endpoint.Ready {
			return endpoint
		}
	}
	if len(endpoints) > 0 {
		return endpoints[0]
	}
	return k8s.ServiceEndpoint{}
}

// updateLoadBalancer updates the load balancer for a service
func (drm *DynamicRouteManager) updateLoadBalancer(service *k8s.DiscoveredService) {
	drm.lbMutex.Lock()
	defer drm.lbMutex.Unlock()

	lb := &LoadBalancer{
		Strategy:    service.LoadBalancing,
		Endpoints:   service.Endpoints,
		LastUpdated: time.Now(),
	}

	drm.loadBalancers[service.Name] = lb
	log.Printf("Load balancer updated for service %s: %d endpoints", service.Name, len(service.Endpoints))
}

// Simple authentication check
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

// Simplified proxy implementation
func (drm *DynamicRouteManager) proxyRequest(w http.ResponseWriter, r *http.Request, route *DynamicRouteInfo, endpoint k8s.ServiceEndpoint) error {
	targetURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", endpoint.IP, endpoint.Port),
	}

	log.Printf("Creating proxy to: %s", targetURL.String())

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Host = targetURL.Host
		req.URL.Scheme = targetURL.Scheme
		req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
		req.Header.Set("X-Gateway-Service", route.ServiceName)
		req.Header.Set("X-Gateway-Endpoint", endpoint.IP)
		req.Host = targetURL.Host

		log.Printf("Proxy director: %s %s -> %s", req.Method, req.URL.String(), targetURL.String())
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Proxy error for service %s: %v", route.ServiceName, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	log.Printf("Executing proxy for %s %s to %s", r.Method, r.URL.Path, targetURL.String())

	proxy.ServeHTTP(w, r)

	return nil
}

// updateRouteStats updates statistics for a route
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

// incrementSuccessStats increments success statistics
func (drm *DynamicRouteManager) incrementSuccessStats() {
	drm.statsMutex.Lock()
	drm.stats.SuccessRequests++
	drm.statsMutex.Unlock()
}

// incrementErrorStats increments error statistics
func (drm *DynamicRouteManager) incrementErrorStats() {
	drm.statsMutex.Lock()
	drm.stats.ErrorRequests++
	drm.statsMutex.Unlock()
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
