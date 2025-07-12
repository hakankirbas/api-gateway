// pkg/logger/lokiHook.go
package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type LokiHook struct {
	endpoint string
	client   *http.Client
}

type LokiLogEntry struct {
	Streams []LokiStream `json:"streams"`
}

type LokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

func NewLokiHook(endpoint string) *LokiHook {
	return &LokiHook{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (h *LokiHook) Fire(entry *LogEntry) error {
	// Convert log entry to Loki format
	labels := map[string]string{
		"app":       "api-gateway",
		"level":     entry.Level,
		"component": entry.Component,
	}

	if entry.CorrelationID != "" {
		labels["correlation_id"] = entry.CorrelationID
	}

	// Convert entry to JSON line
	logLine, _ := json.Marshal(entry)
	timestamp := fmt.Sprintf("%d", entry.Timestamp.UnixNano())

	lokiEntry := LokiLogEntry{
		Streams: []LokiStream{
			{
				Stream: labels,
				Values: [][]string{
					{timestamp, string(logLine)},
				},
			},
		},
	}

	// Send to Loki
	jsonData, _ := json.Marshal(lokiEntry)
	req, err := http.NewRequest("POST", h.endpoint+"/loki/api/v1/push", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	_, err = h.client.Do(req)
	return err
}

func (h *LokiHook) Levels() []LogLevel {
	return []LogLevel{DEBUG, INFO, WARN, ERROR, FATAL}
}
