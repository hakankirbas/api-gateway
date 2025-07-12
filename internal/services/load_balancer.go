package services

import (
	"api-gateway/internal/k8s"
	"crypto/rand"
	"math/big"
	"sync"
	"time"
)

// LoadBalancerStrategy defines the interface for load balancing strategies
type LoadBalancerStrategy interface {
	SelectEndpoint(endpoints []k8s.ServiceEndpoint) k8s.ServiceEndpoint
	Name() string
}

// LoadBalancer manages load balancing for services
type LoadBalancer struct {
	strategy    LoadBalancerStrategy
	serviceName string
	endpoints   []k8s.ServiceEndpoint
	stats       *LoadBalancerStats
	mutex       sync.RWMutex
}

// LoadBalancerStats tracks load balancer statistics
type LoadBalancerStats struct {
	TotalRequests      int64            `json:"total_requests"`
	EndpointRequests   map[string]int64 `json:"endpoint_requests"`
	LastSelected       string           `json:"last_selected"`
	LastSelectedTime   time.Time        `json:"last_selected_time"`
	HealthyEndpoints   int              `json:"healthy_endpoints"`
	UnhealthyEndpoints int              `json:"unhealthy_endpoints"`
}

// NewLoadBalancer creates a new load balancer with the specified strategy
func NewLoadBalancer(serviceName string, strategy LoadBalancerStrategy) *LoadBalancer {
	return &LoadBalancer{
		strategy:    strategy,
		serviceName: serviceName,
		endpoints:   make([]k8s.ServiceEndpoint, 0),
		stats: &LoadBalancerStats{
			EndpointRequests: make(map[string]int64),
		},
	}
}

// UpdateEndpoints updates the list of available endpoints
func (lb *LoadBalancer) UpdateEndpoints(endpoints []k8s.ServiceEndpoint) {
	lb.mutex.Lock()
	defer lb.mutex.Unlock()

	lb.endpoints = endpoints
	lb.updateStats()
}

// SelectEndpoint selects an endpoint using the configured strategy
func (lb *LoadBalancer) SelectEndpoint() k8s.ServiceEndpoint {
	lb.mutex.Lock()
	defer lb.mutex.Unlock()

	healthyEndpoints := lb.getHealthyEndpoints()
	if len(healthyEndpoints) == 0 {
		return k8s.ServiceEndpoint{}
	}

	selected := lb.strategy.SelectEndpoint(healthyEndpoints)

	// Update statistics
	lb.stats.TotalRequests++
	endpointKey := selected.IP + ":" + string(rune(selected.Port))
	lb.stats.EndpointRequests[endpointKey]++
	lb.stats.LastSelected = endpointKey
	lb.stats.LastSelectedTime = time.Now()

	return selected
}

// GetStats returns current load balancer statistics
func (lb *LoadBalancer) GetStats() LoadBalancerStats {
	lb.mutex.RLock()
	defer lb.mutex.RUnlock()

	// Return a copy to avoid race conditions
	stats := LoadBalancerStats{
		TotalRequests:      lb.stats.TotalRequests,
		EndpointRequests:   make(map[string]int64),
		LastSelected:       lb.stats.LastSelected,
		LastSelectedTime:   lb.stats.LastSelectedTime,
		HealthyEndpoints:   lb.stats.HealthyEndpoints,
		UnhealthyEndpoints: lb.stats.UnhealthyEndpoints,
	}

	for k, v := range lb.stats.EndpointRequests {
		stats.EndpointRequests[k] = v
	}

	return stats
}

func (lb *LoadBalancer) getHealthyEndpoints() []k8s.ServiceEndpoint {
	var healthy []k8s.ServiceEndpoint
	for _, endpoint := range lb.endpoints {
		if endpoint.Ready {
			healthy = append(healthy, endpoint)
		}
	}
	return healthy
}

func (lb *LoadBalancer) updateStats() {
	healthy := 0
	unhealthy := 0

	for _, endpoint := range lb.endpoints {
		if endpoint.Ready {
			healthy++
		} else {
			unhealthy++
		}
	}

	lb.stats.HealthyEndpoints = healthy
	lb.stats.UnhealthyEndpoints = unhealthy
}

// RoundRobinStrategy implements round-robin load balancing
type RoundRobinStrategy struct {
	current int
	mutex   sync.Mutex
}

func NewRoundRobinStrategy() *RoundRobinStrategy {
	return &RoundRobinStrategy{current: 0}
}

func (rr *RoundRobinStrategy) SelectEndpoint(endpoints []k8s.ServiceEndpoint) k8s.ServiceEndpoint {
	if len(endpoints) == 0 {
		return k8s.ServiceEndpoint{}
	}

	rr.mutex.Lock()
	defer rr.mutex.Unlock()

	endpoint := endpoints[rr.current%len(endpoints)]
	rr.current++

	return endpoint
}

func (rr *RoundRobinStrategy) Name() string {
	return "round-robin"
}

// WeightedRoundRobinStrategy implements weighted round-robin load balancing
type WeightedRoundRobinStrategy struct {
	weights map[string]int
	current int
	mutex   sync.Mutex
}

func NewWeightedRoundRobinStrategy(weights map[string]int) *WeightedRoundRobinStrategy {
	return &WeightedRoundRobinStrategy{
		weights: weights,
		current: 0,
	}
}

func (wrr *WeightedRoundRobinStrategy) SelectEndpoint(endpoints []k8s.ServiceEndpoint) k8s.ServiceEndpoint {
	if len(endpoints) == 0 {
		return k8s.ServiceEndpoint{}
	}

	wrr.mutex.Lock()
	defer wrr.mutex.Unlock()

	// Simple weighted round-robin implementation
	// In production, you might want a more sophisticated algorithm
	totalWeight := 0
	for _, endpoint := range endpoints {
		key := endpoint.IP + ":" + string(rune(endpoint.Port))
		if weight, exists := wrr.weights[key]; exists {
			totalWeight += weight
		} else {
			totalWeight += 1 // Default weight
		}
	}

	if totalWeight == 0 {
		return endpoints[0]
	}

	target := wrr.current % totalWeight
	currentWeight := 0

	for _, endpoint := range endpoints {
		key := endpoint.IP + ":" + string(rune(endpoint.Port))
		weight := 1
		if w, exists := wrr.weights[key]; exists {
			weight = w
		}

		currentWeight += weight
		if currentWeight > target {
			wrr.current++
			return endpoint
		}
	}

	return endpoints[0]
}

func (wrr *WeightedRoundRobinStrategy) Name() string {
	return "weighted-round-robin"
}

// RandomStrategy implements random load balancing
type RandomStrategy struct{}

func NewRandomStrategy() *RandomStrategy {
	return &RandomStrategy{}
}

func (r *RandomStrategy) SelectEndpoint(endpoints []k8s.ServiceEndpoint) k8s.ServiceEndpoint {
	if len(endpoints) == 0 {
		return k8s.ServiceEndpoint{}
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(endpoints))))
	if err != nil {
		// Fallback to first endpoint if random generation fails
		return endpoints[0]
	}

	return endpoints[n.Int64()]
}

func (r *RandomStrategy) Name() string {
	return "random"
}

// LeastConnectionsStrategy implements least connections load balancing
type LeastConnectionsStrategy struct {
	connections map[string]int64
	mutex       sync.RWMutex
}

func NewLeastConnectionsStrategy() *LeastConnectionsStrategy {
	return &LeastConnectionsStrategy{
		connections: make(map[string]int64),
	}
}

func (lc *LeastConnectionsStrategy) SelectEndpoint(endpoints []k8s.ServiceEndpoint) k8s.ServiceEndpoint {
	if len(endpoints) == 0 {
		return k8s.ServiceEndpoint{}
	}

	lc.mutex.RLock()
	defer lc.mutex.RUnlock()

	var selectedEndpoint k8s.ServiceEndpoint
	minConnections := int64(-1)

	for _, endpoint := range endpoints {
		key := endpoint.IP + ":" + string(rune(endpoint.Port))
		connections := lc.connections[key]

		if minConnections == -1 || connections < minConnections {
			minConnections = connections
			selectedEndpoint = endpoint
		}
	}

	return selectedEndpoint
}

func (lc *LeastConnectionsStrategy) IncrementConnections(endpoint k8s.ServiceEndpoint) {
	lc.mutex.Lock()
	defer lc.mutex.Unlock()

	key := endpoint.IP + ":" + string(rune(endpoint.Port))
	lc.connections[key]++
}

func (lc *LeastConnectionsStrategy) DecrementConnections(endpoint k8s.ServiceEndpoint) {
	lc.mutex.Lock()
	defer lc.mutex.Unlock()

	key := endpoint.IP + ":" + string(rune(endpoint.Port))
	if lc.connections[key] > 0 {
		lc.connections[key]--
	}
}

func (lc *LeastConnectionsStrategy) Name() string {
	return "least-connections"
}

// LoadBalancerManager manages load balancers for multiple services
type LoadBalancerManager struct {
	loadBalancers map[string]*LoadBalancer
	mutex         sync.RWMutex
}

func NewLoadBalancerManager() *LoadBalancerManager {
	return &LoadBalancerManager{
		loadBalancers: make(map[string]*LoadBalancer),
	}
}

func (lbm *LoadBalancerManager) GetOrCreateLoadBalancer(serviceName, strategyName string) *LoadBalancer {
	lbm.mutex.Lock()
	defer lbm.mutex.Unlock()

	if lb, exists := lbm.loadBalancers[serviceName]; exists {
		return lb
	}

	var strategy LoadBalancerStrategy
	switch strategyName {
	case "weighted-round-robin":
		strategy = NewWeightedRoundRobinStrategy(nil)
	case "random":
		strategy = NewRandomStrategy()
	case "least-connections":
		strategy = NewLeastConnectionsStrategy()
	default:
		strategy = NewRoundRobinStrategy()
	}

	lb := NewLoadBalancer(serviceName, strategy)
	lbm.loadBalancers[serviceName] = lb

	return lb
}

func (lbm *LoadBalancerManager) UpdateServiceEndpoints(serviceName string, endpoints []k8s.ServiceEndpoint) {
	lbm.mutex.RLock()
	lb, exists := lbm.loadBalancers[serviceName]
	lbm.mutex.RUnlock()

	if exists {
		lb.UpdateEndpoints(endpoints)
	}
}

func (lbm *LoadBalancerManager) GetLoadBalancerStats(serviceName string) (LoadBalancerStats, bool) {
	lbm.mutex.RLock()
	defer lbm.mutex.RUnlock()

	if lb, exists := lbm.loadBalancers[serviceName]; exists {
		return lb.GetStats(), true
	}

	return LoadBalancerStats{}, false
}

func (lbm *LoadBalancerManager) GetAllStats() map[string]LoadBalancerStats {
	lbm.mutex.RLock()
	defer lbm.mutex.RUnlock()

	stats := make(map[string]LoadBalancerStats)
	for serviceName, lb := range lbm.loadBalancers {
		stats[serviceName] = lb.GetStats()
	}

	return stats
}
