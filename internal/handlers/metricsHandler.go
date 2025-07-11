package handlers

import (
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// MetricsHandler provides basic Prometheus-style metrics
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	now := time.Now().Unix()

	metrics := fmt.Sprintf(`# HELP gateway_info Information about the gateway
# TYPE gateway_info gauge
gateway_info{version="1.0.0",service="api-gateway"} 1

# HELP gateway_uptime_seconds Total uptime of the gateway in seconds
# TYPE gateway_uptime_seconds counter
gateway_uptime_seconds %d

# HELP gateway_memory_alloc_bytes Number of bytes allocated and still in use
# TYPE gateway_memory_alloc_bytes gauge
gateway_memory_alloc_bytes %d

# HELP gateway_memory_total_alloc_bytes Total number of bytes allocated
# TYPE gateway_memory_total_alloc_bytes counter
gateway_memory_total_alloc_bytes %d

# HELP gateway_memory_sys_bytes Number of bytes obtained from system
# TYPE gateway_memory_sys_bytes gauge
gateway_memory_sys_bytes %d

# HELP gateway_gc_runs_total Total number of GC runs
# TYPE gateway_gc_runs_total counter
gateway_gc_runs_total %d

# HELP gateway_goroutines Current number of goroutines
# TYPE gateway_goroutines gauge
gateway_goroutines %d

# HELP gateway_requests_total Total number of HTTP requests processed
# TYPE gateway_requests_total counter
gateway_requests_total{method="GET",status="200"} 0
gateway_requests_total{method="POST",status="200"} 0

# HELP gateway_request_duration_seconds Request duration in seconds
# TYPE gateway_request_duration_seconds histogram
gateway_request_duration_seconds_bucket{le="0.1"} 0
gateway_request_duration_seconds_bucket{le="0.5"} 0
gateway_request_duration_seconds_bucket{le="1.0"} 0
gateway_request_duration_seconds_bucket{le="+Inf"} 0
gateway_request_duration_seconds_sum 0
gateway_request_duration_seconds_count 0
`,
		now,
		m.Alloc,
		m.TotalAlloc,
		m.Sys,
		m.NumGC,
		runtime.NumGoroutine(),
	)

	fmt.Fprint(w, metrics)
}
