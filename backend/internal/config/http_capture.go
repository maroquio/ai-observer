package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tobilg/ai-observer/internal/logger"
)

// Capture modes of the HTTP capture proxy (AI_OBSERVER_HTTP_CAPTURE_MODE)
const (
	// CaptureSafe stores bodies with secrets redacted (default)
	CaptureSafe = "safe"
	// CaptureMetadataOnly stores no bodies, only metadata (status, timing,
	// model, usage, sizes, hashes)
	CaptureMetadataOnly = "metadata_only"
	// CaptureFullRaw also stores the bodies as observed, unredacted, in
	// local files (explicit opt-in for controlled classroom use)
	CaptureFullRaw = "full_raw"
)

// Defaults of the HTTP capture proxy
const (
	DefaultHTTPProxyPort          = 8787
	DefaultHTTPProxyUpstream      = "https://api.anthropic.com"
	DefaultHTTPCaptureMaxBody     = 32 << 20 // per stored body
	DefaultHTTPCaptureQueueSize   = 1024
	DefaultHTTPCaptureStorageSize = 2 << 30 // blob directory
	maxHTTPCaptureQueueSize       = 1 << 20
)

// HTTPProxyConfig configures the Anthropic HTTP capture proxy
// (internal/httpproxy). It is off by default: nothing listens and nothing
// is captured until it is enabled.
type HTTPProxyConfig struct {
	// Enabled starts the capture proxy with the server
	// (AI_OBSERVER_HTTP_PROXY_ENABLED, or serve --http-proxy)
	Enabled bool
	// Host is the interface of the proxy's listeners
	// (AI_OBSERVER_HTTP_PROXY_HOST, default 127.0.0.1). Other than loopback
	// needs AllowRemote.
	Host string
	// Port is the shared listener for manual use
	// (ANTHROPIC_BASE_URL=http://127.0.0.1:8787 claude); 0 disables it, so
	// only launcher runs (ai-observer run), each on its own port, capture
	Port int
	// Upstream is the fixed destination (AI_OBSERVER_HTTP_PROXY_UPSTREAM):
	// clients cannot choose another one
	Upstream string
	// CaptureMode is CaptureSafe, CaptureMetadataOnly or CaptureFullRaw
	CaptureMode string
	// CaptureDir holds the stored bodies (AI_OBSERVER_HTTP_CAPTURE_DIR;
	// default: http-captures next to the database)
	CaptureDir string
	// MaxBodyBytes bounds each stored body; traffic is never truncated
	MaxBodyBytes int64
	// QueueSize bounds the capture records waiting to be stored; when full,
	// records are dropped and counted, traffic is never delayed
	QueueSize int
	// RetentionDays deletes capture runs older than this; -1 inherits
	// AI_OBSERVER_RETENTION_DAYS, 0 keeps them
	RetentionDays int
	// StorageLimit bounds the bytes of stored bodies: the oldest finished
	// runs are deleted above it; 0 means no limit
	StorageLimit int64
	// AllowRemote permits a Host other than loopback
	// (AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE)
	AllowRemote bool
}

// loadHTTPProxy reads the HTTP capture settings
func loadHTTPProxy() HTTPProxyConfig {
	limit := int64(DefaultHTTPCaptureStorageSize)
	if v := strings.TrimSpace(os.Getenv("AI_OBSERVER_HTTP_CAPTURE_STORAGE_LIMIT")); v != "" {
		switch strings.ToLower(v) {
		case "0", "off", "none":
			limit = 0
		default:
			if size := getByteSize("AI_OBSERVER_HTTP_CAPTURE_STORAGE_LIMIT"); size > 0 {
				limit = size
			}
		}
	}
	maxBody := int64(DefaultHTTPCaptureMaxBody)
	if size := getByteSize("AI_OBSERVER_HTTP_CAPTURE_MAX_BODY_BYTES"); size > 0 {
		maxBody = size
	}
	return HTTPProxyConfig{
		Enabled:       getEnvBool("AI_OBSERVER_HTTP_PROXY_ENABLED", false),
		Host:          getHost("AI_OBSERVER_HTTP_PROXY_HOST", "127.0.0.1"),
		Port:          getOptionalPort("AI_OBSERVER_HTTP_PROXY_PORT", DefaultHTTPProxyPort),
		Upstream:      strings.TrimSuffix(strings.TrimSpace(getEnv("AI_OBSERVER_HTTP_PROXY_UPSTREAM", DefaultHTTPProxyUpstream)), "/"),
		CaptureMode:   getCaptureMode(),
		CaptureDir:    strings.TrimSpace(os.Getenv("AI_OBSERVER_HTTP_CAPTURE_DIR")),
		MaxBodyBytes:  maxBody,
		QueueSize:     getEnvInt("AI_OBSERVER_HTTP_CAPTURE_QUEUE_SIZE", DefaultHTTPCaptureQueueSize, 16, maxHTTPCaptureQueueSize),
		RetentionDays: getEnvInt("AI_OBSERVER_HTTP_CAPTURE_RETENTION_DAYS", -1, -1, maxRetentionDays),
		StorageLimit:  limit,
		AllowRemote:   getEnvBool("AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE", false),
	}
}

// getCaptureMode reads AI_OBSERVER_HTTP_CAPTURE_MODE. An unknown mode is
// logged and replaced by the safe default.
func getCaptureMode() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("AI_OBSERVER_HTTP_CAPTURE_MODE")))
	switch value {
	case "":
		return CaptureSafe
	case CaptureSafe, CaptureMetadataOnly, CaptureFullRaw:
		return value
	case "metadata", "metadata-only":
		return CaptureMetadataOnly
	case "raw", "full-raw":
		return CaptureFullRaw
	default:
		logger.Warn("Ignoring invalid setting, using the default",
			"key", "AI_OBSERVER_HTTP_CAPTURE_MODE", "value", value, "default", CaptureSafe,
			"expected", "safe, metadata_only or full_raw")
		return CaptureSafe
	}
}

// HTTPCaptureDir returns the directory of stored capture bodies:
// AI_OBSERVER_HTTP_CAPTURE_DIR, or "http-captures" next to the database
func (c *Config) HTTPCaptureDir() string {
	if c.HTTPProxy.CaptureDir != "" {
		return c.HTTPProxy.CaptureDir
	}
	return filepath.Join(filepath.Dir(c.DatabasePath), "http-captures")
}

// HTTPCaptureRetentionDays returns the capture retention in effect: its own
// setting, or AI_OBSERVER_RETENTION_DAYS when it inherits
func (c *Config) HTTPCaptureRetentionDays() int {
	if c.HTTPProxy.RetentionDays >= 0 {
		return c.HTTPProxy.RetentionDays
	}
	return c.RetentionDays
}
