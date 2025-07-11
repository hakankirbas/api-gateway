package services

import (
	"api-gateway/internal/k8s"
	"api-gateway/internal/middleware"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"github.com/gorilla/mux"
)

// RouterIntegration handles the integration between service discovery and the HTTP router
type RouterIntegration struct {
	router           *mux.Router
	discoveryManager *DiscoveryManager
	dynamicRoutes    map[string]http.HandlerFunc
	routesMutex      sync.RWMutex
	authMiddleware   *middleware.AuthMiddleware
}

// NewRouterIntegration creates a new router integration
func NewRouterIntegration(router *mux.Router, discoveryManager *DiscoveryManager, authMiddleware *middleware.AuthMiddleware) *RouterIntegration {
	integration := &RouterIntegration{
		router:           router,
		discoveryManager: discoveryManager,
		dynamicRoutes:    make(map[string]http.HandlerFunc),
		authMiddleware:   authMiddleware,
	}

	discoveryManager.AddEventProcessor(integration)

	return integration
}

// ProcessServiceEvent implements EventProcessor interface
func (ri *RouterIntegration) ProcessServiceEvent(event k8s.ServiceEvent) error {
	switch event.Type {
	case k8s.ServiceAdded:
		return ri.addRoute(event.Service)
	case k8s.ServiceModified:
		return ri.updateRoute(event.Service)
	case k8s.ServiceDeleted:
		return ri.removeRoute(event.Service)
	}
	return nil
}

// addRoute adds a new dynamic route to the router
func (ri *RouterIntegration) addRoute(service *k8s.DiscoveredService) error {
	ri.routesMutex.Lock()
	defer ri.routesMutex.Unlock()

	routeKey := fmt.Sprintf("%s:%s", service.Method, service.Path)

	proxyHandler := ri.createProxyHandler(service)

	var finalHandler http.HandlerFunc
	if service.AuthRequired {
		wrappedHandler := ri.authMiddleware.Middleware(true)(proxyHandler)
		finalHandler = func(w http.ResponseWriter, r *http.Request) {
			wrappedHandler.ServeHTTP(w, r)
		}
	} else {
		finalHandler = proxyHandler
	}

	ri.dynamicRoutes[routeKey] = finalHandler

	ri.router.HandleFunc(service.Path, finalHandler).Methods(service.Method)

	log.Printf("Dynamic route added: %s %s -> %s (auth: %v)",
		service.Method, service.Path, service.Name, service.AuthRequired)

	return nil
}

// updateRoute updates an existing dynamic route
func (ri *RouterIntegration) updateRoute(service *k8s.DiscoveredService) error {
	ri.routesMutex.Lock()
	defer ri.routesMutex.Unlock()

	routeKey := fmt.Sprintf("%s:%s", service.Method, service.Path)

	proxyHandler := ri.createProxyHandler(service)

	var finalHandler http.HandlerFunc
	if service.AuthRequired {
		wrappedHandler := ri.authMiddleware.Middleware(true)(proxyHandler)
		finalHandler = func(w http.ResponseWriter, r *http.Request) {
			wrappedHandler.ServeHTTP(w, r)
		}
	} else {
		finalHandler = proxyHandler
	}

	ri.dynamicRoutes[routeKey] = finalHandler

	log.Printf("Dynamic route updated: %s %s -> %s (auth: %v)",
		service.Method, service.Path, service.Name, service.AuthRequired)

	return nil
}

// removeRoute removes a dynamic route from the router
func (ri *RouterIntegration) removeRoute(service *k8s.DiscoveredService) error {
	ri.routesMutex.Lock()
	defer ri.routesMutex.Unlock()

	routeKey := fmt.Sprintf("%s:%s", service.Method, service.Path)

	unavailableHandler := func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Route removed: %s %s", r.Method, r.URL.Path)
		http.Error(w, "Service Unavailable - Route Removed", http.StatusServiceUnavailable)
	}

	ri.dynamicRoutes[routeKey] = unavailableHandler

	log.Printf("Dynamic route removed: %s %s", service.Method, service.Path)

	return nil
}

// createProxyHandler creates a proxy handler for a discovered service
func (ri *RouterIntegration) createProxyHandler(service *k8s.DiscoveredService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		endpoints := ri.discoveryManager.GetServiceEndpoints(service.Name)
		if len(endpoints) == 0 {
			log.Printf("No healthy endpoints available for service: %s", service.Name)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}

		endpoint := ri.selectEndpoint(endpoints, service.LoadBalancing)

		targetURL := &url.URL{
			Scheme: "http", // TODO: Support HTTPS
			Host:   fmt.Sprintf("%s:%d", endpoint.IP, endpoint.Port),
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
			req.Header.Set("X-Gateway-Service", service.Name)
			req.Header.Set("X-Gateway-Endpoint", endpoint.IP)
		}

		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy error for service %s (endpoint %s:%d): %v",
				service.Name, endpoint.IP, endpoint.Port, err)
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		}

		log.Printf("Proxying request: %s %s to %s:%d (service: %s)",
			r.Method, r.URL.Path, endpoint.IP, endpoint.Port, service.Name)

		proxy.ServeHTTP(w, r)
	}
}

// selectEndpoint selects an endpoint based on the load balancing strategy
func (ri *RouterIntegration) selectEndpoint(endpoints []k8s.ServiceEndpoint, strategy string) k8s.ServiceEndpoint {
	if len(endpoints) == 0 {
		return k8s.ServiceEndpoint{}
	}

	switch strategy {
	case "round-robin", "":
		// TODO: Implement proper round-robin with state
		return endpoints[0]
	case "random":
		// TODO: Implement random selection
		return endpoints[0]
	case "least-connections":
		// TODO: Implement least connections
		return endpoints[0]
	default:
		return endpoints[0]
	}
}

// GetDynamicRoutes returns information about all dynamic routes
func (ri *RouterIntegration) GetDynamicRoutes() map[string]interface{} {
	ri.routesMutex.RLock()
	defer ri.routesMutex.RUnlock()

	routes := make(map[string]interface{})
	discoveredRoutes := ri.discoveryManager.GetRoutes()

	for key, route := range discoveredRoutes {
		routeInfo := map[string]interface{}{
			"path":          route.Path,
			"method":        route.Method,
			"service_name":  route.ServiceName,
			"namespace":     route.Namespace,
			"auth_required": route.AuthRequired,
			"endpoints":     len(route.Endpoints),
			"healthy_endpoints": func() int {
				count := 0
				for _, ep := range route.Endpoints {
					if ep.Ready {
						count++
					}
				}
				return count
			}(),
			"last_updated": route.LastUpdated,
		}
		routes[key] = routeInfo
	}

	return routes
}
