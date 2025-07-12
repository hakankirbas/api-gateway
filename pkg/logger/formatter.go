package logger

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JSONFormatter formats logs as JSON
type JSONFormatter struct{}

func (f *JSONFormatter) Format(entry *LogEntry) ([]byte, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	// Add newline for readability
	return append(data, '\n'), nil
}

// TextFormatter formats logs as human-readable text
type TextFormatter struct{}

func (f *TextFormatter) Format(entry *LogEntry) ([]byte, error) {
	var fields []string

	if entry.CorrelationID != "" {
		fields = append(fields, fmt.Sprintf("correlation_id=%s", entry.CorrelationID))
	}
	if entry.RequestID != "" {
		fields = append(fields, fmt.Sprintf("request_id=%s", entry.RequestID))
	}
	if entry.Component != "" {
		fields = append(fields, fmt.Sprintf("component=%s", entry.Component))
	}
	if entry.UserID != "" {
		fields = append(fields, fmt.Sprintf("user_id=%s", entry.UserID))
	}
	if entry.Method != "" && entry.Path != "" {
		fields = append(fields, fmt.Sprintf("method=%s", entry.Method))
		fields = append(fields, fmt.Sprintf("path=%s", entry.Path))
	}
	if entry.StatusCode != 0 {
		fields = append(fields, fmt.Sprintf("status=%d", entry.StatusCode))
	}
	if entry.Duration != "" {
		fields = append(fields, fmt.Sprintf("duration=%s", entry.Duration))
	}
	if entry.ClientIP != "" {
		fields = append(fields, fmt.Sprintf("client_ip=%s", entry.ClientIP))
	}
	if entry.Error != "" {
		fields = append(fields, fmt.Sprintf("error=%s", entry.Error))
	}

	// Add custom fields
	for key, value := range entry.Fields {
		fields = append(fields, fmt.Sprintf("%s=%v", key, value))
	}

	fieldStr := ""
	if len(fields) > 0 {
		fieldStr = " " + strings.Join(fields, " ")
	}

	result := fmt.Sprintf("%s [%s] [%s] %s%s\n",
		entry.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		entry.Level,
		entry.Service,
		entry.Message,
		fieldStr,
	)

	return []byte(result), nil
}
