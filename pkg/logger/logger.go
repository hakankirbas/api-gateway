package logger

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// LogLevel represents the severity level of logs
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

var logLevelNames = map[LogLevel]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
	FATAL: "FATAL",
}

var logLevelMap = map[string]LogLevel{
	"debug": DEBUG,
	"info":  INFO,
	"warn":  WARN,
	"error": ERROR,
	"fatal": FATAL,
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp     time.Time              `json:"timestamp"`
	Level         string                 `json:"level"`
	Message       string                 `json:"message"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
	UserID        string                 `json:"user_id,omitempty"`
	Service       string                 `json:"service"`
	Component     string                 `json:"component,omitempty"`
	Method        string                 `json:"method,omitempty"`
	Path          string                 `json:"path,omitempty"`
	StatusCode    int                    `json:"status_code,omitempty"`
	Duration      string                 `json:"duration,omitempty"`
	Error         string                 `json:"error,omitempty"`
	StackTrace    string                 `json:"stack_trace,omitempty"`
	RequestID     string                 `json:"request_id,omitempty"`
	ClientIP      string                 `json:"client_ip,omitempty"`
	UserAgent     string                 `json:"user_agent,omitempty"`
	Fields        map[string]interface{} `json:"fields,omitempty"`
}

// Hook interface for extending logging functionality
type Hook interface {
	Fire(entry *LogEntry) error
	Levels() []LogLevel
}

// Formatter interface for different log formats
type Formatter interface {
	Format(entry *LogEntry) ([]byte, error)
}

// Logger provides structured logging capabilities
type Logger struct {
	level     LogLevel
	service   string
	component string
	ctx       context.Context
	output    io.Writer
	mu        sync.RWMutex
	hooks     []Hook
	formatter Formatter
}

// Config holds logger configuration
type Config struct {
	Level       string `yaml:"level" json:"level"`
	Format      string `yaml:"format" json:"format"`
	Service     string `yaml:"service" json:"service"`
	Output      string `yaml:"output" json:"output"`
	EnableHooks bool   `yaml:"enable_hooks" json:"enable_hooks"`
}

// NewLogger creates a new structured logger
func NewLogger(config Config) *Logger {
	level := INFO
	if l, exists := logLevelMap[strings.ToLower(config.Level)]; exists {
		level = l
	}

	output := os.Stdout
	if config.Output == "stderr" {
		output = os.Stderr
	}

	var formatter Formatter
	switch strings.ToLower(config.Format) {
	case "json":
		formatter = &JSONFormatter{}
	default:
		formatter = &TextFormatter{}
	}

	logger := &Logger{
		level:     level,
		service:   config.Service,
		output:    output,
		formatter: formatter,
		hooks:     make([]Hook, 0),
	}

	if config.EnableHooks {
		// Add default hooks
		logger.AddHook(&ErrorTrackingHook{})
	}

	return logger
}

// AddHook adds a hook to the logger
func (l *Logger) AddHook(hook Hook) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hooks = append(l.hooks, hook)
}

// SetLevel sets the logging level
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current logging level
func (l *Logger) GetLevel() LogLevel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// WithContext returns a new logger with context information
func (l *Logger) WithContext(ctx context.Context) *Logger {
	return &Logger{
		level:     l.level,
		service:   l.service,
		component: l.component,
		ctx:       ctx,
		output:    l.output,
		hooks:     l.hooks,
		formatter: l.formatter,
	}
}

// WithComponent returns a new logger with component information
func (l *Logger) WithComponent(component string) *Logger {
	return &Logger{
		level:     l.level,
		service:   l.service,
		component: component,
		ctx:       l.ctx,
		output:    l.output,
		hooks:     l.hooks,
		formatter: l.formatter,
	}
}

// log is the internal logging method
func (l *Logger) log(level LogLevel, msg string, fields map[string]interface{}) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if level < l.level {
		return
	}

	entry := &LogEntry{
		Timestamp: time.Now().UTC(),
		Level:     logLevelNames[level],
		Message:   msg,
		Service:   l.service,
		Component: l.component,
		Fields:    fields,
	}

	// Extract common fields from context if available
	if l.ctx != nil {
		if correlationID := GetCorrelationID(l.ctx); correlationID != "" {
			entry.CorrelationID = correlationID
		}
		if userID := GetUserID(l.ctx); userID != "" {
			entry.UserID = userID
		}
		if requestID := GetRequestID(l.ctx); requestID != "" {
			entry.RequestID = requestID
		}
	}

	// Extract error details
	if err, ok := fields["error"].(error); ok {
		entry.Error = err.Error()
		if level >= ERROR {
			entry.StackTrace = getStackTrace()
		}
		delete(fields, "error")
	}

	// Extract HTTP fields
	if method, ok := fields["method"].(string); ok {
		entry.Method = method
		delete(fields, "method")
	}
	if path, ok := fields["path"].(string); ok {
		entry.Path = path
		delete(fields, "path")
	}
	if statusCode, ok := fields["status_code"].(int); ok {
		entry.StatusCode = statusCode
		delete(fields, "status_code")
	}
	if duration, ok := fields["duration"].(time.Duration); ok {
		entry.Duration = duration.String()
		delete(fields, "duration")
	}
	if clientIP, ok := fields["client_ip"].(string); ok {
		entry.ClientIP = clientIP
		delete(fields, "client_ip")
	}
	if userAgent, ok := fields["user_agent"].(string); ok {
		entry.UserAgent = userAgent
		delete(fields, "user_agent")
	}

	// Fire hooks
	for _, hook := range l.hooks {
		if l.shouldFireHook(hook, level) {
			if err := hook.Fire(entry); err != nil {
				// Log hook errors to stderr to avoid infinite loops
				log.Printf("Hook error: %v", err)
			}
		}
	}

	// Format and write log
	formatted, err := l.formatter.Format(entry)
	if err != nil {
		log.Printf("Log formatting error: %v", err)
		return
	}

	l.output.Write(formatted)

	// For FATAL level, exit the program
	if level == FATAL {
		os.Exit(1)
	}
}

func (l *Logger) shouldFireHook(hook Hook, level LogLevel) bool {
	levels := hook.Levels()
	if len(levels) == 0 {
		return true // Fire for all levels if none specified
	}

	for _, hookLevel := range levels {
		if hookLevel == level {
			return true
		}
	}
	return false
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, fields ...map[string]interface{}) {
	l.log(DEBUG, msg, l.mergeFields(fields...))
}

// Info logs an info message
func (l *Logger) Info(msg string, fields ...map[string]interface{}) {
	l.log(INFO, msg, l.mergeFields(fields...))
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, fields ...map[string]interface{}) {
	l.log(WARN, msg, l.mergeFields(fields...))
}

// Error logs an error message
func (l *Logger) Error(msg string, fields ...map[string]interface{}) {
	l.log(ERROR, msg, l.mergeFields(fields...))
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(msg string, fields ...map[string]interface{}) {
	l.log(FATAL, msg, l.mergeFields(fields...))
}

func (l *Logger) mergeFields(fieldMaps ...map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for _, fields := range fieldMaps {
		for k, v := range fields {
			result[k] = v
		}
	}
	return result
}

// getStackTrace captures the current stack trace
func getStackTrace() string {
	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])

	var stack []string
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.File, "runtime/") &&
			!strings.Contains(frame.File, "logger/") {
			stack = append(stack, fmt.Sprintf("%s:%d %s", frame.File, frame.Line, frame.Function))
		}
		if !more {
			break
		}
	}
	return strings.Join(stack, "\n")
}
