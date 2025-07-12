package middleware

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState int

const (
	StateClosed CircuitBreakerState = iota
	StateHalfOpen
	StateOpen
)

func (s CircuitBreakerState) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateHalfOpen:
		return "HALF_OPEN"
	case StateOpen:
		return "OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreakerConfig holds configuration for a circuit breaker
type CircuitBreakerConfig struct {
	MaxRequests   uint32                                                              `json:"max_requests"` // Max requests allowed in half-open state
	Interval      time.Duration                                                       `json:"interval"`     // Statistical window duration
	Timeout       time.Duration                                                       `json:"timeout"`      // Time after which open circuit goes to half-open
	ReadyToTrip   func(counts Counts) bool                                            `json:"-"`            // Function to determine when to trip
	OnStateChange func(name string, from CircuitBreakerState, to CircuitBreakerState) `json:"-"`
	IsSuccessful  func(err error) bool                                                `json:"-"` // Function to determine if request was successful
}

// Counts holds statistics about requests
type Counts struct {
	Requests             uint32 `json:"requests"`
	TotalSuccesses       uint32 `json:"total_successes"`
	TotalFailures        uint32 `json:"total_failures"`
	ConsecutiveSuccesses uint32 `json:"consecutive_successes"`
	ConsecutiveFailures  uint32 `json:"consecutive_failures"`
}

// ErrorRate returns the current error rate (failures/requests)
func (c Counts) ErrorRate() float64 {
	if c.Requests == 0 {
		return 0.0
	}
	return float64(c.TotalFailures) / float64(c.Requests)
}

// SuccessRate returns the current success rate (successes/requests)
func (c Counts) SuccessRate() float64 {
	if c.Requests == 0 {
		return 0.0
	}
	return float64(c.TotalSuccesses) / float64(c.Requests)
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name          string
	maxRequests   uint32
	interval      time.Duration
	timeout       time.Duration
	readyToTrip   func(counts Counts) bool
	isSuccessful  func(err error) bool
	onStateChange func(name string, from CircuitBreakerState, to CircuitBreakerState)

	mutex      sync.Mutex
	state      CircuitBreakerState
	generation uint64
	counts     Counts
	expiry     time.Time
}

var (
	ErrTooManyRequests = errors.New("circuit breaker: too many requests")
	ErrOpenState       = errors.New("circuit breaker: open state")
)

// NewCircuitBreaker creates a new circuit breaker with the given config
func NewCircuitBreaker(name string, config CircuitBreakerConfig) *CircuitBreaker {
	cb := &CircuitBreaker{
		name:        name,
		maxRequests: config.MaxRequests,
		interval:    config.Interval,
		timeout:     config.Timeout,
	}

	if config.ReadyToTrip == nil {
		cb.readyToTrip = defaultReadyToTrip
	} else {
		cb.readyToTrip = config.ReadyToTrip
	}

	if config.IsSuccessful == nil {
		cb.isSuccessful = defaultIsSuccessful
	} else {
		cb.isSuccessful = config.IsSuccessful
	}

	cb.onStateChange = config.OnStateChange

	cb.toNewGeneration(time.Now())

	return cb
}

// Execute runs the given function if the circuit breaker allows it
func (cb *CircuitBreaker) Execute(fn func() (interface{}, error)) (interface{}, error) {
	generation, err := cb.beforeRequest()
	if err != nil {
		return nil, err
	}

	defer func() {
		if r := recover(); r != nil {
			cb.afterRequest(generation, false)
			panic(r)
		}
	}()

	result, err := fn()
	cb.afterRequest(generation, cb.isSuccessful(err))
	return result, err
}

// State returns the current state of the circuit breaker
func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, _ := cb.currentState(now)
	return state
}

// Counts returns the current counts
func (cb *CircuitBreaker) Counts() Counts {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	return cb.counts
}

// Name returns the name of the circuit breaker
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

func (cb *CircuitBreaker) beforeRequest() (uint64, error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)

	if state == StateOpen {
		return generation, ErrOpenState
	} else if state == StateHalfOpen && cb.counts.Requests >= cb.maxRequests {
		return generation, ErrTooManyRequests
	}

	cb.counts.Requests++
	return generation, nil
}

func (cb *CircuitBreaker) afterRequest(before uint64, success bool) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)
	if generation != before {
		return
	}

	if success {
		cb.onSuccess(state, now)
	} else {
		cb.onFailure(state, now)
	}
}

func (cb *CircuitBreaker) onSuccess(state CircuitBreakerState, now time.Time) {
	cb.counts.TotalSuccesses++
	cb.counts.ConsecutiveSuccesses++
	cb.counts.ConsecutiveFailures = 0

	if state == StateHalfOpen {
		cb.setState(StateClosed, now)
	}
}

func (cb *CircuitBreaker) onFailure(state CircuitBreakerState, now time.Time) {
	cb.counts.TotalFailures++
	cb.counts.ConsecutiveFailures++
	cb.counts.ConsecutiveSuccesses = 0

	if cb.readyToTrip(cb.counts) {
		cb.setState(StateOpen, now)
	}
}

func (cb *CircuitBreaker) currentState(now time.Time) (CircuitBreakerState, uint64) {
	switch cb.state {
	case StateClosed:
		if !cb.expiry.IsZero() && cb.expiry.Before(now) {
			cb.toNewGeneration(now)
		}
	case StateOpen:
		if cb.expiry.Before(now) {
			cb.setState(StateHalfOpen, now)
		}
	}
	return cb.state, cb.generation
}

func (cb *CircuitBreaker) setState(state CircuitBreakerState, now time.Time) {
	if cb.state == state {
		return
	}

	prev := cb.state
	cb.state = state

	cb.toNewGeneration(now)

	if cb.onStateChange != nil {
		cb.onStateChange(cb.name, prev, state)
	}
}

func (cb *CircuitBreaker) toNewGeneration(now time.Time) {
	cb.generation++
	cb.counts = Counts{}

	var zero time.Time
	switch cb.state {
	case StateClosed:
		if cb.interval == 0 {
			cb.expiry = zero
		} else {
			cb.expiry = now.Add(cb.interval)
		}
	case StateOpen:
		cb.expiry = now.Add(cb.timeout)
	default: // StateHalfOpen
		cb.expiry = zero
	}
}

// Default implementation for readyToTrip
func defaultReadyToTrip(counts Counts) bool {
	return counts.ConsecutiveFailures > 5
}

// Default implementation for isSuccessful
func defaultIsSuccessful(err error) bool {
	return err == nil
}

// CircuitBreakerManager manages multiple circuit breakers
type CircuitBreakerManager struct {
	breakers map[string]*CircuitBreaker
	mutex    sync.RWMutex
	config   CircuitBreakerConfig
}

// NewCircuitBreakerManager creates a new circuit breaker manager
func NewCircuitBreakerManager(config CircuitBreakerConfig) *CircuitBreakerManager {
	// Set default values if not provided
	if config.MaxRequests == 0 {
		config.MaxRequests = 1
	}
	if config.Interval == 0 {
		config.Interval = 60 * time.Second
	}
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}
	if config.ReadyToTrip == nil {
		config.ReadyToTrip = func(counts Counts) bool {
			return counts.ConsecutiveFailures > 5 || counts.ErrorRate() > 0.6
		}
	}
	if config.OnStateChange == nil {
		config.OnStateChange = func(name string, from CircuitBreakerState, to CircuitBreakerState) {
			fmt.Printf("Circuit breaker '%s' changed from %s to %s\n", name, from, to)
		}
	}

	return &CircuitBreakerManager{
		breakers: make(map[string]*CircuitBreaker),
		config:   config,
	}
}

// GetCircuitBreaker returns a circuit breaker for the given service
func (cbm *CircuitBreakerManager) GetCircuitBreaker(serviceName string) *CircuitBreaker {
	cbm.mutex.RLock()
	cb, exists := cbm.breakers[serviceName]
	cbm.mutex.RUnlock()

	if exists {
		return cb
	}

	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()

	// Double-check after acquiring write lock
	if cb, exists := cbm.breakers[serviceName]; exists {
		return cb
	}

	cb = NewCircuitBreaker(serviceName, cbm.config)
	cbm.breakers[serviceName] = cb
	return cb
}

// GetAllStates returns the states of all circuit breakers
func (cbm *CircuitBreakerManager) GetAllStates() map[string]CircuitBreakerState {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	states := make(map[string]CircuitBreakerState)
	for name, cb := range cbm.breakers {
		states[name] = cb.State()
	}
	return states
}

// GetAllCounts returns the counts of all circuit breakers
func (cbm *CircuitBreakerManager) GetAllCounts() map[string]Counts {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	counts := make(map[string]Counts)
	for name, cb := range cbm.breakers {
		counts[name] = cb.Counts()
	}
	return counts
}

// CircuitBreakerStats provides comprehensive statistics
type CircuitBreakerStats struct {
	Name        string              `json:"name"`
	State       CircuitBreakerState `json:"state"`
	Counts      Counts              `json:"counts"`
	ErrorRate   float64             `json:"error_rate"`
	SuccessRate float64             `json:"success_rate"`
	MaxRequests uint32              `json:"max_requests"`
	Interval    time.Duration       `json:"interval"`
	Timeout     time.Duration       `json:"timeout"`
}

// GetStats returns comprehensive statistics for all circuit breakers
func (cbm *CircuitBreakerManager) GetStats() map[string]CircuitBreakerStats {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	stats := make(map[string]CircuitBreakerStats)
	for name, cb := range cbm.breakers {
		counts := cb.Counts()
		stats[name] = CircuitBreakerStats{
			Name:        name,
			State:       cb.State(),
			Counts:      counts,
			ErrorRate:   counts.ErrorRate(),
			SuccessRate: counts.SuccessRate(),
			MaxRequests: cb.maxRequests,
			Interval:    cb.interval,
			Timeout:     cb.timeout,
		}
	}
	return stats
}
