package services

import (
	"api-gateway/internal/config"
	"api-gateway/internal/k8s"
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// DiscoveryManager manages service discovery and dynamic routing
type DiscoveryManager struct {
	config           *config.Config
	k8sClient        *k8s.Client
	serviceDiscovery *k8s.ServiceDiscovery
	routes           map[string]*DynamicRoute
	routesMutex      sync.RWMutex
	eventProcessors  []EventProcessor
	stopCh           chan struct{}
	started          bool
}

// DynamicRoute represents a dynamically discovered route
type DynamicRoute struct {
	Path         string                 `json:"path"`
	Method       string                 `json:"method"`
	ServiceName  string                 `json:"service_name"`
	Namespace    string                 `json:"namespace"`
	AuthRequired bool                   `json:"auth_required"`
	Endpoints    []k8s.ServiceEndpoint  `json:"endpoints"`
	Service      *k8s.DiscoveredService `json:"service"`
	LastUpdated  time.Time              `json:"last_updated"`
}

// EventProcessor interface for handling service discovery events
type EventProcessor interface {
	ProcessServiceEvent(event k8s.ServiceEvent) error
}

// NewDiscoveryManager creates a new discovery manager
func NewDiscoveryManager(cfg *config.Config) *DiscoveryManager {
	return &DiscoveryManager{
		config:          cfg,
		routes:          make(map[string]*DynamicRoute),
		eventProcessors: make([]EventProcessor, 0),
		stopCh:          make(chan struct{}),
	}
}

// Start initializes and starts the discovery manager
func (dm *DiscoveryManager) Start(ctx context.Context) error {
	if dm.started {
		return fmt.Errorf("discovery manager already started")
	}

	log.Println("Starting Discovery Manager...")

	if dm.config.Kubernetes.Enabled {
		if err := dm.initializeKubernetes(); err != nil {
			return fmt.Errorf("failed to initialize Kubernetes: %w", err)
		}

		if dm.config.Kubernetes.ServiceDiscovery {
			if err := dm.startServiceDiscovery(ctx); err != nil {
				return fmt.Errorf("failed to start service discovery: %w", err)
			}
		}
	}

	go dm.processEvents()

	dm.started = true
	log.Println("Discovery Manager started successfully")
	return nil
}

// Stop stops the discovery manager
func (dm *DiscoveryManager) Stop() {
	if !dm.started {
		return
	}

	log.Println("Stopping Discovery Manager...")

	if dm.serviceDiscovery != nil {
		dm.serviceDiscovery.Stop()
	}

	close(dm.stopCh)
	dm.started = false

	log.Println("Discovery Manager stopped")
}

// GetRoutes returns all current dynamic routes
func (dm *DiscoveryManager) GetRoutes() map[string]*DynamicRoute {
	dm.routesMutex.RLock()
	defer dm.routesMutex.RUnlock()

	routes := make(map[string]*DynamicRoute)
	for k, v := range dm.routes {
		routes[k] = v
	}
	return routes
}

// GetRoute returns a specific route
func (dm *DiscoveryManager) GetRoute(path string) (*DynamicRoute, bool) {
	dm.routesMutex.RLock()
	defer dm.routesMutex.RUnlock()
	route, exists := dm.routes[path]
	return route, exists
}

// AddEventProcessor adds an event processor
func (dm *DiscoveryManager) AddEventProcessor(processor EventProcessor) {
	dm.eventProcessors = append(dm.eventProcessors, processor)
}

// GetDiscoveredServices returns all discovered services
func (dm *DiscoveryManager) GetDiscoveredServices() map[string]*k8s.DiscoveredService {
	if dm.serviceDiscovery == nil {
		return make(map[string]*k8s.DiscoveredService)
	}
	return dm.serviceDiscovery.GetServices()
}

// IsKubernetesEnabled returns whether Kubernetes integration is enabled
func (dm *DiscoveryManager) IsKubernetesEnabled() bool {
	return dm.config.Kubernetes.Enabled
}

// GetServiceEndpoints returns healthy endpoints for a service
func (dm *DiscoveryManager) GetServiceEndpoints(serviceName string) []k8s.ServiceEndpoint {
	dm.routesMutex.RLock()
	defer dm.routesMutex.RUnlock()

	for _, route := range dm.routes {
		if route.ServiceName == serviceName {
			var healthyEndpoints []k8s.ServiceEndpoint
			for _, endpoint := range route.Endpoints {
				if endpoint.Ready {
					healthyEndpoints = append(healthyEndpoints, endpoint)
				}
			}
			return healthyEndpoints
		}
	}
	return nil
}

// initializeKubernetes sets up the Kubernetes client
func (dm *DiscoveryManager) initializeKubernetes() error {
	log.Println("Initializing Kubernetes client...")

	clientConfig := k8s.ClientConfig{
		InCluster:  dm.config.Kubernetes.InCluster,
		KubeConfig: dm.config.Kubernetes.KubeconfigPath,
		Namespace:  dm.config.Kubernetes.Namespace,
	}

	if dm.config.Kubernetes.KubeconfigPath == "" {
		clientConfig = k8s.AutoDetectConfig(dm.config.Kubernetes.Namespace)
	}

	client, err := k8s.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	dm.k8sClient = client
	log.Printf("Kubernetes client initialized successfully (namespace: %s)", client.GetNamespace())
	return nil
}

// startServiceDiscovery initializes and starts service discovery
func (dm *DiscoveryManager) startServiceDiscovery(ctx context.Context) error {
	log.Println("Starting Kubernetes service discovery...")

	dm.serviceDiscovery = k8s.NewServiceDiscovery(dm.k8sClient)

	if err := dm.serviceDiscovery.Start(ctx); err != nil {
		return fmt.Errorf("failed to start service discovery: %w", err)
	}

	log.Println("Service discovery started successfully")
	return nil
}

// processEvents processes service discovery events
func (dm *DiscoveryManager) processEvents() {
	if dm.serviceDiscovery == nil {
		return
	}

	log.Println("Starting event processing...")

	for {
		select {
		case event := <-dm.serviceDiscovery.GetEventChannel():
			dm.handleServiceEvent(event)
		case <-dm.stopCh:
			log.Println("Stopping event processing")
			return
		}
	}
}

// handleServiceEvent handles a service discovery event
func (dm *DiscoveryManager) handleServiceEvent(event k8s.ServiceEvent) {
	log.Printf("Processing service event: %s for service %s", event.Type, event.Service.Name)

	dm.updateRoutes(event)

	for _, processor := range dm.eventProcessors {
		if err := processor.ProcessServiceEvent(event); err != nil {
			log.Printf("Error processing event with processor: %v", err)
		}
	}
}

// updateRoutes updates internal route table based on service events
func (dm *DiscoveryManager) updateRoutes(event k8s.ServiceEvent) {
	dm.routesMutex.Lock()
	defer dm.routesMutex.Unlock()

	service := event.Service
	if service == nil {
		return
	}

	routeKey := fmt.Sprintf("%s:%s", service.Method, service.Path)

	switch event.Type {
	case k8s.ServiceAdded, k8s.ServiceModified:
		route := &DynamicRoute{
			Path:         service.Path,
			Method:       service.Method,
			ServiceName:  service.Name,
			Namespace:    service.Namespace,
			AuthRequired: service.AuthRequired,
			Endpoints:    service.Endpoints,
			Service:      service,
			LastUpdated:  time.Now(),
		}
		dm.routes[routeKey] = route
		log.Printf("Route updated: %s %s -> %s (%d endpoints)",
			route.Method, route.Path, route.ServiceName, len(route.Endpoints))

	case k8s.ServiceDeleted:
		delete(dm.routes, routeKey)
		log.Printf("Route removed: %s %s", service.Method, service.Path)
	}
}

// GetStats returns discovery manager statistics
func (dm *DiscoveryManager) GetStats() map[string]interface{} {
	dm.routesMutex.RLock()
	defer dm.routesMutex.RUnlock()

	stats := map[string]interface{}{
		"kubernetes_enabled": dm.config.Kubernetes.Enabled,
		"service_discovery":  dm.config.Kubernetes.ServiceDiscovery,
		"namespace":          dm.config.Kubernetes.Namespace,
		"total_routes":       len(dm.routes),
		"started":            dm.started,
	}

	if dm.serviceDiscovery != nil {
		services := dm.serviceDiscovery.GetServices()
		stats["discovered_services"] = len(services)

		totalEndpoints := 0
		healthyEndpoints := 0
		for _, route := range dm.routes {
			totalEndpoints += len(route.Endpoints)
			for _, endpoint := range route.Endpoints {
				if endpoint.Ready {
					healthyEndpoints++
				}
			}
		}
		stats["total_endpoints"] = totalEndpoints
		stats["healthy_endpoints"] = healthyEndpoints
	}

	return stats
}
