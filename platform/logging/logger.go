package logger

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// logHolder atomically stores the active *zap.Logger so that concurrent
// Initialize / SetOutput writes and in-flight reads (applogger.Log.Info(...)
// from any package) are race-free (platform-L9). The previous design used a
// plain package var written under an RWMutex but read without any lock at all.
var logPtr atomic.Pointer[zap.Logger]

func init() {
	nop := zap.NewNop()
	logPtr.Store(nop)
}

// Logger is the accessor exposed as the package-level `Log` value. Its methods
// always delegate to the atomically-current logger, so callers that write
// `applogger.Log.Info(...)` keep working AND see an Initialize() that happens
// after they were compiled in. Callers that need to pass the logger to a
// function expecting a *zap.Logger should call applogger.Current() instead of
// capturing applogger.Log at construction time (see NewAuditor).
type Logger struct{}

// Current returns the atomically-current *zap.Logger. Use this when a
// *zap.Logger value must be captured (e.g. Named/With); prefer the Logger
// methods on Log for direct logging.
func Current() *zap.Logger {
	return logPtr.Load()
}

func storeLogger(l *zap.Logger) {
	if l == nil {
		nop := zap.NewNop()
		l = nop
	}
	logPtr.Store(l)
}

// Log is the process-wide logger accessor. It is safe for concurrent use and
// always reflects the most recent Initialize/SetOutput.
var Log = Logger{}

// Named returns a child logger; because it resolves the current logger at call
// time, callers that hold the returned *zap.Logger still pin a snapshot —
// prefer re-resolving per request for long-lived holders.
func (Logger) Named(s string) *zap.Logger {
	return Current().Named(s)
}

func (Logger) With(fields ...zap.Field) *zap.Logger {
	return Current().With(fields...)
}

func (Logger) Debug(msg string, fields ...zap.Field)  { Current().Debug(msg, fields...) }
func (Logger) Info(msg string, fields ...zap.Field)   { Current().Info(msg, fields...) }
func (Logger) Warn(msg string, fields ...zap.Field)   { Current().Warn(msg, fields...) }
func (Logger) Error(msg string, fields ...zap.Field)  { Current().Error(msg, fields...) }
func (Logger) DPanic(msg string, fields ...zap.Field) { Current().DPanic(msg, fields...) }
func (Logger) Panic(msg string, fields ...zap.Field)  { Current().Panic(msg, fields...) }
func (Logger) Fatal(msg string, fields ...zap.Field)  { Current().Fatal(msg, fields...) }

func (Logger) Sync() error { return Current().Sync() }

func (Logger) Core() zapcore.Core { return Current().Core() }

func (Logger) Check(lvl zapcore.Level, msg string) *zapcore.CheckedEntry {
	return Current().Check(lvl, msg)
}

// Sensitive data patterns for redaction
var (
	authHeaderRegex = regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-_\.]+`)
	basicAuthRegex  = regexp.MustCompile(`Basic\s+[A-Za-z0-9+/=]+`)
	jwtRegex        = regexp.MustCompile(`eyJ[A-Za-z0-9\-_]+\.eyJ[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+`)
	apiKeyRegex     = regexp.MustCompile(`api[_-]?key["\']?\s*[:=]\s*["\']?([A-Za-z0-9\-_\.]+)`)
	tokenRegex      = regexp.MustCompile(`token["\']?\s*[:=]\s*["\']?([A-Za-z0-9\-_\.]+)`)
	passwordRegex   = regexp.MustCompile(`password["\']?\s*[:=]\s*["\']?([^\s"\']+)`)
	dsnRegex        = regexp.MustCompile(`:[^:@]+@`)
)

// SwapLogger atomically replaces the global logger and returns the previous
// one. It is intended for tests that need to temporarily swap the logger
// (platform-L9: Log is no longer an assignable *zap.Logger value). Production
// code should use Initialize / SetOutput.
func SwapLogger(l *zap.Logger) *zap.Logger {
	prev := logPtr.Load()
	if l == nil {
		nop := zap.NewNop()
		l = nop
	}
	logPtr.Store(l)
	return prev
}

// SetLoggerForTest is a convenience wrapper around SwapLogger that restores the
// previous logger on test cleanup.
func SetLoggerForTest(t interface {
	Cleanup(func())
}, l *zap.Logger) {
	prev := SwapLogger(l)
	t.Cleanup(func() { logPtr.Store(prev) })
}

// Initialize initializes the global logger
func Initialize(level string, format string) error {
	var zapLevel zapcore.Level
	switch strings.ToLower(level) {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	var config zap.Config
	if format == "json" {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
	}

	config.Level = zap.NewAtomicLevelAt(zapLevel)
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.EncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	var err error
	logger, err := config.Build(
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	if service := os.Getenv("SERVICE_NAME"); service != "" {
		logger = logger.With(zap.String("service", service))
	}

	storeLogger(logger)

	return nil
}

// Sanitize removes sensitive information from log messages
func Sanitize(input string) string {
	// Redact authorization headers
	input = authHeaderRegex.ReplaceAllString(input, "Bearer ***REDACTED***")
	// Redact Basic auth headers
	input = basicAuthRegex.ReplaceAllString(input, "Basic ***REDACTED***")
	// Redact JWT tokens
	input = jwtRegex.ReplaceAllString(input, "***JWT_REDACTED***")
	// Redact API keys
	input = apiKeyRegex.ReplaceAllString(input, `api_key:"***REDACTED***"`)
	// Redact tokens
	input = tokenRegex.ReplaceAllString(input, `token:"***REDACTED***"`)
	// Redact passwords
	input = passwordRegex.ReplaceAllString(input, `password:"***REDACTED***"`)
	// Redact DSN passwords
	input = dsnRegex.ReplaceAllString(input, ":***REDACTED***@")

	return input
}

// sanitizeField sanitizes a single zap field value if it contains sensitive data
func sanitizeField(field zap.Field) zap.Field {
	if field.Type == zapcore.StringType {
		sanitized := Sanitize(field.String)
		if sanitized != field.String {
			return zap.String(field.Key, sanitized)
		}
	}
	return field
}

// TruncateString truncates a string to a maximum length and adds ellipsis if needed
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// SanitizeAndTruncate both sanitizes and truncates a string for logging
func SanitizeAndTruncate(s string, maxLen int) string {
	sanitized := Sanitize(s)
	return TruncateString(sanitized, maxLen)
}

// SafeLogger provides safe logging methods that automatically sanitize input
type SafeLogger struct {
	logger *zap.Logger
}

// NewSafeLogger creates a new safe logger wrapper
func NewSafeLogger(logger *zap.Logger) *SafeLogger {
	return &SafeLogger{logger: logger}
}

// sanitizeFields sanitizes all string-type zap fields
func sanitizeFields(fields []zap.Field) []zap.Field {
	sanitized := make([]zap.Field, len(fields))
	for i, f := range fields {
		sanitized[i] = sanitizeField(f)
	}
	return sanitized
}

// Debug logs a debug message with sanitization
func (sl *SafeLogger) Debug(msg string, fields ...zap.Field) {
	sl.logger.Debug(Sanitize(msg), sanitizeFields(fields)...)
}

// Info logs an info message with sanitization
func (sl *SafeLogger) Info(msg string, fields ...zap.Field) {
	sl.logger.Info(Sanitize(msg), sanitizeFields(fields)...)
}

// Warn logs a warning message with sanitization
func (sl *SafeLogger) Warn(msg string, fields ...zap.Field) {
	sl.logger.Warn(Sanitize(msg), sanitizeFields(fields)...)
}

// Error logs an error message with sanitization
func (sl *SafeLogger) Error(msg string, fields ...zap.Field) {
	sl.logger.Error(Sanitize(msg), sanitizeFields(fields)...)
}

// Fatal logs a fatal message with sanitization and exits
func (sl *SafeLogger) Fatal(msg string, fields ...zap.Field) {
	sl.logger.Fatal(Sanitize(msg), sanitizeFields(fields)...)
}

// With creates a child logger with additional fields
func (sl *SafeLogger) With(fields ...zap.Field) *SafeLogger {
	return NewSafeLogger(sl.logger.With(fields...))
}

// Sync flushes any buffered log entries
func Sync() error {
	return Current().Sync()
}

// InitializeFromEnv initializes the logger from environment variables
func InitializeFromEnv() error {
	level := os.Getenv("LOG_LEVEL")
	if level == "" {
		level = "info"
	}

	format := os.Getenv("LOG_FORMAT")
	if format == "" {
		format = "json"
	}

	return Initialize(level, format)
}

// MustInitializeFromEnv initializes the package logger or falls back to a no-op
// logger if zap setup fails. It returns the initialization error for callers
// that want to report startup diagnostics without risking nil logger panics.
func MustInitializeFromEnv() error {
	if err := InitializeFromEnv(); err != nil {
		nop := zap.NewNop()
		storeLogger(nop)
		return err
	}
	return nil
}

// SetOutput sets the output destination for the logger
func SetOutput(w io.Writer) error {
	if Current() == nil {
		return fmt.Errorf("logger not initialized")
	}

	config := zap.NewProductionConfig()
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stderr"}

	// Create encoder
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	encoder := zapcore.NewJSONEncoder(encoderConfig)
	core := zapcore.NewCore(encoder, zapcore.AddSync(w), zapcore.InfoLevel)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	if service := os.Getenv("SERVICE_NAME"); service != "" {
		logger = logger.With(zap.String("service", service))
	}
	storeLogger(logger)

	return nil
}
