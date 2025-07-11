package k8s

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
)

// ServiceDiscovery manages dynamic service discovery using Kubernetes API
type ServiceDiscovery struct {
	client    *Client
	services  map[string]*DiscoveredService
	endpoints map[string]*corev1.Endpoints
	mutex     sync.RWMutex
	stopCh    chan struct{}
	eventCh   chan ServiceEvent
	informers []cache.SharedIndexInformer
}

// DiscoveredService represents a service discovered from Kubernetes
type DiscoveredService struct {
	Name          string            `json:"name"`
	Namespace     string            `json:"namespace"`
	Path          string            `json:"path"`
	Method        string            `json:"method"`
	AuthRequired  bool              `json:"auth_required"`
	LoadBalancing string            `json:"load_balancing"`
	Annotations   map[string]string `json:"annotations"`
	Endpoints     []ServiceEndpoint `json:"endpoints"`
	LastUpdated   time.Time         `json:"last_updated"`
}

// ServiceEndpoint represents a backend endpoint for a service
type ServiceEndpoint struct {
	IP       string `json:"ip"`
	Port     int32  `json:"port"`
	Ready    bool   `json:"ready"`
	NodeName string `json:"node_name,omitempty"`
}

// ServiceEvent represents a change in service discovery
type ServiceEvent struct {
	Type      ServiceEventType   `json:"type"`
	Service   *DiscoveredService `json:"service"`
	Timestamp time.Time          `json:"timestamp"`
}

// ServiceEventType represents the type of service event
type ServiceEventType string

const (
	ServiceAdded    ServiceEventType = "ADDED"
	ServiceModified ServiceEventType = "MODIFIED"
	ServiceDeleted  ServiceEventType = "DELETED"
)

// Annotation keys for service configuration
const (
	AnnotationEnabled       = "gateway.io/enabled"
	AnnotationPath          = "gateway.io/path"
	AnnotationMethod        = "gateway.io/method"
	AnnotationAuthRequired  = "gateway.io/auth-required"
	AnnotationLoadBalancing = "gateway.io/load-balancing"
)

// NewServiceDiscovery creates a new service discovery manager
func NewServiceDiscovery(client *Client) *ServiceDiscovery {
	return &ServiceDiscovery{
		client:    client,
		services:  make(map[string]*DiscoveredService),
		endpoints: make(map[string]*corev1.Endpoints),
		stopCh:    make(chan struct{}),
		eventCh:   make(chan ServiceEvent, 100),
	}
}

// Start begins watching for service and endpoint changes
func (sd *ServiceDiscovery) Start(ctx context.Context) error {
	log.Println("Starting service discovery...")

	// Start service informer
	serviceInformer := sd.createServiceInformer()
	sd.informers = append(sd.informers, serviceInformer)

	// Start endpoint informer
	endpointInformer := sd.createEndpointInformer()
	sd.informers = append(sd.informers, endpointInformer)

	// Start all informers
	for _, informer := range sd.informers {
		go informer.Run(sd.stopCh)
	}

	// Wait for cache sync
	log.Println("Waiting for cache sync...")
	for _, informer := range sd.informers {
		if !cache.WaitForCacheSync(sd.stopCh, informer.HasSynced) {
			return fmt.Errorf("failed to sync cache")
		}
	}

	log.Println("Service discovery started successfully")
	return nil
}

// Stop stops the service discovery
func (sd *ServiceDiscovery) Stop() {
	log.Println("Stopping service discovery...")
	close(sd.stopCh)
}

// GetServices returns all discovered services
func (sd *ServiceDiscovery) GetServices() map[string]*DiscoveredService {
	sd.mutex.RLock()
	defer sd.mutex.RUnlock()

	// Return a copy to avoid race conditions
	services := make(map[string]*DiscoveredService)
	for k, v := range sd.services {
		services[k] = v
	}
	return services
}

// GetService returns a specific discovered service
func (sd *ServiceDiscovery) GetService(name string) (*DiscoveredService, bool) {
	sd.mutex.RLock()
	defer sd.mutex.RUnlock()
	service, exists := sd.services[name]
	return service, exists
}

// GetEventChannel returns the channel for service events
func (sd *ServiceDiscovery) GetEventChannel() <-chan ServiceEvent {
	return sd.eventCh
}

// createServiceInformer creates an informer for Kubernetes services
func (sd *ServiceDiscovery) createServiceInformer() cache.SharedIndexInformer {
	listWatcher := cache.NewListWatchFromClient(
		sd.client.Clientset.CoreV1().RESTClient(),
		"services",
		sd.client.Namespace,
		fields.Everything(),
	)

	informer := cache.NewSharedIndexInformer(
		listWatcher,
		&corev1.Service{},
		30*time.Second, // Resync period
		cache.Indexers{},
	)

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if service, ok := obj.(*corev1.Service); ok {
				sd.handleServiceEvent(service, ServiceAdded)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if service, ok := newObj.(*corev1.Service); ok {
				sd.handleServiceEvent(service, ServiceModified)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if service, ok := obj.(*corev1.Service); ok {
				sd.handleServiceEvent(service, ServiceDeleted)
			}
		},
	})

	return informer
}

// createEndpointInformer creates an informer for Kubernetes endpoints
func (sd *ServiceDiscovery) createEndpointInformer() cache.SharedIndexInformer {
	listWatcher := cache.NewListWatchFromClient(
		sd.client.Clientset.CoreV1().RESTClient(),
		"endpoints",
		sd.client.Namespace,
		fields.Everything(),
	)

	informer := cache.NewSharedIndexInformer(
		listWatcher,
		&corev1.Endpoints{},
		30*time.Second, // Resync period
		cache.Indexers{},
	)

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if endpoints, ok := obj.(*corev1.Endpoints); ok {
				sd.handleEndpointEvent(endpoints)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if endpoints, ok := newObj.(*corev1.Endpoints); ok {
				sd.handleEndpointEvent(endpoints)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if endpoints, ok := obj.(*corev1.Endpoints); ok {
				sd.handleEndpointEvent(endpoints)
			}
		},
	})

	return informer
}

// handleServiceEvent processes service events
func (sd *ServiceDiscovery) handleServiceEvent(service *corev1.Service, eventType ServiceEventType) {
	// Check if service should be discovered
	if !sd.shouldDiscoverService(service) {
		return
	}

	sd.mutex.Lock()
	defer sd.mutex.Unlock()

	serviceName := service.Name

	if eventType == ServiceDeleted {
		delete(sd.services, serviceName)
		log.Printf("Service removed from discovery: %s", serviceName)
	} else {
		// Create or update discovered service
		discoveredService := sd.createDiscoveredService(service)
		sd.services[serviceName] = discoveredService

		// Update endpoints if we have them
		if endpoints, exists := sd.endpoints[serviceName]; exists {
			discoveredService.Endpoints = sd.convertEndpoints(endpoints)
		}

		log.Printf("Service %s in discovery: %s -> %s %s", eventType, serviceName, discoveredService.Method, discoveredService.Path)
	}

	// Send event notification
	select {
	case sd.eventCh <- ServiceEvent{
		Type:      eventType,
		Service:   sd.services[serviceName],
		Timestamp: time.Now(),
	}:
	default:
		log.Printf("Warning: Event channel full, dropping service event for %s", serviceName)
	}
}

// handleEndpointEvent processes endpoint events
func (sd *ServiceDiscovery) handleEndpointEvent(endpoints *corev1.Endpoints) {
	sd.mutex.Lock()
	defer sd.mutex.Unlock()

	serviceName := endpoints.Name
	sd.endpoints[serviceName] = endpoints

	// Update service endpoints if service is discovered
	if service, exists := sd.services[serviceName]; exists {
		service.Endpoints = sd.convertEndpoints(endpoints)
		service.LastUpdated = time.Now()
		log.Printf("Updated endpoints for service: %s (%d endpoints)", serviceName, len(service.Endpoints))
	}
}

// shouldDiscoverService checks if a service should be included in discovery
func (sd *ServiceDiscovery) shouldDiscoverService(service *corev1.Service) bool {
	// Check if service has the gateway annotation
	if enabled, exists := service.Annotations[AnnotationEnabled]; exists {
		return enabled == "true"
	}
	return false
}

// createDiscoveredService converts a Kubernetes service to a discovered service
func (sd *ServiceDiscovery) createDiscoveredService(service *corev1.Service) *DiscoveredService {
	discovered := &DiscoveredService{
		Name:        service.Name,
		Namespace:   service.Namespace,
		Annotations: service.Annotations,
		LastUpdated: time.Now(),
	}

	// Extract routing configuration from annotations
	if path, exists := service.Annotations[AnnotationPath]; exists {
		discovered.Path = path
	} else {
		discovered.Path = "/" + service.Name // Default path
	}

	if method, exists := service.Annotations[AnnotationMethod]; exists {
		discovered.Method = method
	} else {
		discovered.Method = "GET" // Default method
	}

	if authRequired, exists := service.Annotations[AnnotationAuthRequired]; exists {
		discovered.AuthRequired = authRequired == "true"
	}

	if loadBalancing, exists := service.Annotations[AnnotationLoadBalancing]; exists {
		discovered.LoadBalancing = loadBalancing
	} else {
		discovered.LoadBalancing = "round-robin" // Default strategy
	}

	return discovered
}

// convertEndpoints converts Kubernetes endpoints to service endpoints
func (sd *ServiceDiscovery) convertEndpoints(endpoints *corev1.Endpoints) []ServiceEndpoint {
	var serviceEndpoints []ServiceEndpoint

	for _, subset := range endpoints.Subsets {
		port := int32(80) // Default port
		if len(subset.Ports) > 0 {
			port = subset.Ports[0].Port
		}

		// Add ready endpoints
		for _, addr := range subset.Addresses {
			endpoint := ServiceEndpoint{
				IP:    addr.IP,
				Port:  port,
				Ready: true,
			}
			if addr.NodeName != nil {
				endpoint.NodeName = *addr.NodeName
			}
			serviceEndpoints = append(serviceEndpoints, endpoint)
		}

		// Add not ready endpoints
		for _, addr := range subset.NotReadyAddresses {
			endpoint := ServiceEndpoint{
				IP:    addr.IP,
				Port:  port,
				Ready: false,
			}
			if addr.NodeName != nil {
				endpoint.NodeName = *addr.NodeName
			}
			serviceEndpoints = append(serviceEndpoints, endpoint)
		}
	}

	return serviceEndpoints
}

// ListServices lists all services that could be discovered (for debugging)
func (sd *ServiceDiscovery) ListServices() ([]*DiscoveredService, error) {
	services, err := sd.client.Clientset.CoreV1().Services(sd.client.Namespace).List(
		context.TODO(),
		metav1.ListOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	var discoveredServices []*DiscoveredService
	for _, service := range services.Items {
		if sd.shouldDiscoverService(&service) {
			discovered := sd.createDiscoveredService(&service)
			discoveredServices = append(discoveredServices, discovered)
		}
	}

	return discoveredServices, nil
}
