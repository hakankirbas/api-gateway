package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// ErrorTrackingHook tracks errors and sends alerts
type ErrorTrackingHook struct {
	webhookURL    string
	errorCount    map[string]int
	lastAlert     map[string]time.Time
	alertCooldown time.Duration
	mu            sync.RWMutex
	client        *http.Client
}

// AlertPayload represents the structure sent to alerting systems
type AlertPayload struct {
	Timestamp     time.Time              `json:"timestamp"`
	Service       string                 `json:"service"`
	Level         string                 `json:"level"`
	Message       string                 `json:"message"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
	Error         string                 `json:"error,omitempty"`
	StackTrace    string                 `json:"stack_trace,omitempty"`
	Count         int                    `json:"count"`
	Context       map[string]interface{} `json:"context,omitempty"`
}

// NewErrorTrackingHook creates a new error tracking hook
func NewErrorTrackingHook() *ErrorTrackingHook {
	return &ErrorTrackingHook{
		webhookURL:    os.Getenv("ERROR_WEBHOOK_URL"), // Slack, Teams, or custom webhook
		errorCount:    make(map[string]int),
		lastAlert:     make(map[string]time.Time),
		alertCooldown: 5 * time.Minute, // Don't spam alerts
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Fire processes log entries for error tracking
func (h *ErrorTrackingHook) Fire(entry *LogEntry) error {
	// Only process ERROR and FATAL levels
	if entry.Level != "ERROR" && entry.Level != "FATAL" {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Create error key for tracking
	errorKey := h.createErrorKey(entry)

	// Increment error count
	h.errorCount[errorKey]++
	count := h.errorCount[errorKey]

	// Check if we should send an alert
	if h.shouldSendAlert(errorKey, count) {
		h.lastAlert[errorKey] = time.Now()
		go h.sendAlert(entry, count)
	}

	// Clean up old error counts periodically
	h.cleanupOldErrors()

	return nil
}

// Levels returns the log levels this hook should process
func (h *ErrorTrackingHook) Levels() []LogLevel {
	return []LogLevel{ERROR, FATAL}
}

// createErrorKey creates a unique key for error tracking
func (h *ErrorTrackingHook) createErrorKey(entry *LogEntry) string {
	// Combine service, component, and error message for uniqueness
	return fmt.Sprintf("%s:%s:%s", entry.Service, entry.Component, entry.Error)
}

// shouldSendAlert determines if an alert should be sent
func (h *ErrorTrackingHook) shouldSendAlert(errorKey string, count int) bool {
	// Send alert on first error or after cooldown period
	lastAlert, exists := h.lastAlert[errorKey]
	if !exists {
		return true // First occurrence
	}

	// Send alert if cooldown period has passed
	if time.Since(lastAlert) > h.alertCooldown {
		return true
	}

	// Send alert for critical thresholds (10, 50, 100, etc.)
	if count == 10 || count == 50 || count == 100 || (count > 100 && count%100 == 0) {
		return true
	}

	return false
}

// sendAlert sends an alert to the configured webhook
func (h *ErrorTrackingHook) sendAlert(entry *LogEntry, count int) {
	if h.webhookURL == "" {
		return // No webhook configured
	}

	payload := AlertPayload{
		Timestamp:     entry.Timestamp,
		Service:       entry.Service,
		Level:         entry.Level,
		Message:       entry.Message,
		CorrelationID: entry.CorrelationID,
		Error:         entry.Error,
		StackTrace:    entry.StackTrace,
		Count:         count,
		Context: map[string]interface{}{
			"method":      entry.Method,
			"path":        entry.Path,
			"status_code": entry.StatusCode,
			"client_ip":   entry.ClientIP,
			"user_agent":  entry.UserAgent,
			"request_id":  entry.RequestID,
			"user_id":     entry.UserID,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return // Can't marshal, skip alert
	}

	// Send webhook (this is a generic webhook format)
	req, err := http.NewRequest("POST", h.webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

// cleanupOldErrors removes old error counts to prevent memory leaks
func (h *ErrorTrackingHook) cleanupOldErrors() {
	cutoff := time.Now().Add(-1 * time.Hour) // Keep errors for 1 hour

	for errorKey, lastAlert := range h.lastAlert {
		if lastAlert.Before(cutoff) {
			delete(h.errorCount, errorKey)
			delete(h.lastAlert, errorKey)
		}
	}
}

// SlackHook sends alerts specifically formatted for Slack
type SlackHook struct {
	webhookURL string
	client     *http.Client
}

// SlackMessage represents a Slack webhook message
type SlackMessage struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments"`
}

// Attachment represents a Slack message attachment
type Attachment struct {
	Color     string  `json:"color"`
	Title     string  `json:"title"`
	Text      string  `json:"text"`
	Fields    []Field `json:"fields"`
	Timestamp int64   `json:"ts"`
}

// Field represents a Slack attachment field
type Field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// NewSlackHook creates a new Slack alerting hook
func NewSlackHook(webhookURL string) *SlackHook {
	return &SlackHook{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Fire sends error alerts to Slack
func (h *SlackHook) Fire(entry *LogEntry) error {
	if entry.Level != "ERROR" && entry.Level != "FATAL" {
		return nil
	}

	color := "warning"
	if entry.Level == "FATAL" {
		color = "danger"
	}

	fields := []Field{
		{Title: "Service", Value: entry.Service, Short: true},
		{Title: "Level", Value: entry.Level, Short: true},
	}

	if entry.Component != "" {
		fields = append(fields, Field{Title: "Component", Value: entry.Component, Short: true})
	}

	if entry.CorrelationID != "" {
		fields = append(fields, Field{Title: "Correlation ID", Value: entry.CorrelationID, Short: true})
	}

	if entry.Method != "" && entry.Path != "" {
		fields = append(fields, Field{Title: "Endpoint", Value: fmt.Sprintf("%s %s", entry.Method, entry.Path), Short: true})
	}

	if entry.StatusCode != 0 {
		fields = append(fields, Field{Title: "Status Code", Value: fmt.Sprintf("%d", entry.StatusCode), Short: true})
	}

	if entry.ClientIP != "" {
		fields = append(fields, Field{Title: "Client IP", Value: entry.ClientIP, Short: true})
	}

	message := SlackMessage{
		Text: fmt.Sprintf("🚨 %s Error in %s", entry.Level, entry.Service),
		Attachments: []Attachment{
			{
				Color:     color,
				Title:     entry.Message,
				Text:      entry.Error,
				Fields:    fields,
				Timestamp: entry.Timestamp.Unix(),
			},
		},
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", h.webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// Levels returns the log levels this hook should process
func (h *SlackHook) Levels() []LogLevel {
	return []LogLevel{ERROR, FATAL}
}

// MetricsHook tracks error metrics for Prometheus
type MetricsHook struct {
	errorCounter map[string]int
	mu           sync.RWMutex
}

// NewMetricsHook creates a new metrics tracking hook
func NewMetricsHook() *MetricsHook {
	return &MetricsHook{
		errorCounter: make(map[string]int),
	}
}

// Fire processes log entries for metrics tracking
func (h *MetricsHook) Fire(entry *LogEntry) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Create metric key
	key := fmt.Sprintf("%s:%s:%s", entry.Service, entry.Component, entry.Level)
	h.errorCounter[key]++

	return nil
}

// Levels returns all log levels for metrics tracking
func (h *MetricsHook) Levels() []LogLevel {
	return []LogLevel{DEBUG, INFO, WARN, ERROR, FATAL}
}

// GetMetrics returns current error metrics
func (h *MetricsHook) GetMetrics() map[string]int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	metrics := make(map[string]int)
	for k, v := range h.errorCounter {
		metrics[k] = v
	}
	return metrics
}
