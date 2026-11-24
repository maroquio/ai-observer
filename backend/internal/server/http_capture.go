package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/config"
	"github.com/tobilg/ai-observer/internal/diskspace"
	"github.com/tobilg/ai-observer/internal/handlers"
	"github.com/tobilg/ai-observer/internal/httpcapture"
	"github.com/tobilg/ai-observer/internal/httpproxy"
	"github.com/tobilg/ai-observer/internal/logger"
	"github.com/tobilg/ai-observer/internal/version"
	"github.com/tobilg/ai-observer/internal/websocket"
)

// hubNotifier announces stored capture exchanges on the WebSocket
type hubNotifier struct{ hub *websocket.Hub }

func (n hubNotifier) HTTPExchangesStored(_ []string, timestamps []time.Time) {
	n.hub.Broadcast(websocket.HTTPExchangesStored(timestamps))
}

// setupHTTPCapture prepares the HTTP Inspector: the capture proxy when it is
// enabled, and the reading of stored captures in any case. Runs left active
// by a stopped process are marked interrupted.
func (s *Server) setupHTTPCapture() error {
	cfg := s.config.HTTPProxy
	if cfg.CaptureMode == "" {
		cfg.CaptureMode = config.CaptureSafe
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Upstream == "" {
		cfg.Upstream = config.DefaultHTTPProxyUpstream
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if runs, exchanges, err := s.storage.InterruptActiveHTTPCaptures(ctx, time.Now()); err != nil {
		return fmt.Errorf("marking interrupted capture runs: %w", err)
	} else if runs+exchanges > 0 {
		logger.Warn("HTTP capture runs of a stopped process were marked interrupted", "runs", runs, "exchanges", exchanges)
	}

	dir := s.config.HTTPCaptureDir()
	off := api.HTTPInspectorStatus{
		Host:          cfg.Host,
		Port:          cfg.Port,
		Upstream:      cfg.Upstream,
		CaptureMode:   cfg.CaptureMode,
		CaptureDir:    absPath(dir),
		MaxBodyBytes:  cfg.MaxBodyBytes,
		QueueSize:     cfg.QueueSize,
		RetentionDays: s.config.HTTPCaptureRetentionDays(),
		StorageLimit:  cfg.StorageLimit,
		Warnings:      []string{},
	}

	// The capture directory: needed to store bodies, and opened to read the
	// bodies of earlier captures when it exists
	var blobs *httpcapture.BlobStore
	_, statErr := os.Stat(dir)
	if (cfg.Enabled && cfg.CaptureMode != config.CaptureMetadataOnly) || statErr == nil {
		b, err := httpcapture.OpenBlobStore(dir)
		if err != nil {
			if cfg.Enabled && cfg.CaptureMode != config.CaptureMetadataOnly {
				return fmt.Errorf("HTTP capture directory: %w", err)
			}
			logger.Warn("HTTP capture directory not readable: stored bodies are unavailable", "dir", dir, "error", err)
		} else {
			blobs = b
			if err := blobs.ClearTemp(); err != nil {
				logger.Warn("Removing temporary capture files failed", "error", err)
			}
			s.removeOrphanCaptureFiles(ctx, blobs)
		}
	}
	s.captureBlobs = blobs

	if !cfg.Enabled {
		s.httpInspector = handlers.NewHTTPInspector(s.storage, nil, blobs, off)
		return nil
	}
	upstream, err := httpproxy.ValidateUpstream(cfg.Upstream)
	if err != nil {
		return fmt.Errorf("AI_OBSERVER_HTTP_PROXY_UPSTREAM: %w", err)
	}
	if upstream.Host != "api.anthropic.com" {
		logger.Warn("HTTP capture proxy forwards to a non-default upstream", "upstream", upstream.String())
	}
	bodyStore := blobs
	if cfg.CaptureMode == config.CaptureMetadataOnly {
		bodyStore = nil
	}
	capturer := httpcapture.New(httpcapture.Config{
		Mode:          cfg.CaptureMode,
		MaxBodyBytes:  cfg.MaxBodyBytes,
		QueueSize:     cfg.QueueSize,
		Upstream:      upstream.String(),
		ServerVersion: version.Version,
	}, s.storage, bodyStore, s.storage.Redactor(), hubNotifier{s.wsHub})
	manager, err := httpproxy.NewManager(httpproxy.Config{
		Host:         cfg.Host,
		Port:         cfg.Port,
		Upstream:     upstream,
		AllowRemote:  cfg.AllowRemote,
		AllowedHosts: s.config.AllowedHosts,
	}, capturer)
	if err != nil {
		return err
	}
	if cfg.CaptureMode == config.CaptureFullRaw {
		logger.Warn("HTTP capture mode full_raw: request and response bodies are also stored unredacted in the capture directory (credential headers never are)",
			"dir", absPath(dir))
	}
	if !cfg.AllowRemote && s.config.Host != "" && !isLoopbackHost(normalizeHost(s.config.Host)) {
		logger.Warn("The API listens beyond loopback: the HTTP Inspector shows captured prompts and replies to anyone reaching it; set AI_OBSERVER_AUTH_TOKEN")
	}
	s.httpProxy = manager
	s.httpInspector = handlers.NewHTTPInspector(s.storage, manager, blobs, off)
	return nil
}

// removeOrphanCaptureFiles removes the body directories of runs the
// database does not know (deleted while the files could not be)
func (s *Server) removeOrphanCaptureFiles(ctx context.Context, blobs *httpcapture.BlobStore) {
	dirs, err := blobs.RunDirs()
	if err != nil || len(dirs) == 0 {
		return
	}
	ids, err := s.storage.HTTPCaptureRunIDs(ctx)
	if err != nil {
		return
	}
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	for _, d := range dirs {
		if !known[d] {
			if err := blobs.RemoveRun(d); err != nil {
				logger.Warn("Removing orphan capture files failed", "run", d, "error", err)
			}
		}
	}
}

// listenHTTPProxy opens the proxy's shared port (before serving, so a busy
// port fails startup) and serves it
func (s *Server) listenHTTPProxy() error {
	if s.httpProxy == nil {
		return nil
	}
	if err := s.httpProxy.Listen(); err != nil {
		return fmt.Errorf("starting HTTP capture proxy (set AI_OBSERVER_HTTP_PROXY_PORT to another port, or off): %w", err)
	}
	s.httpProxy.Serve()
	return nil
}

// httpInspectorStatus is the proxy's state for GET /api/status
func (s *Server) httpInspectorStatus() api.HTTPInspectorStatus {
	if s.httpInspector == nil {
		return api.HTTPInspectorStatus{Warnings: []string{}}
	}
	return s.httpInspector.Status()
}

// applyHTTPCaptureRetention deletes capture runs older than the capture
// retention, then the oldest finished runs while the stored bodies exceed
// the capture storage limit
func (s *Server) applyHTTPCaptureRetention(ctx context.Context, now time.Time) {
	deletedRuns := 0
	// Deleted rows free blocks for reuse only after a checkpoint
	defer func() {
		if deletedRuns > 0 {
			s.checkpointAfterDeletion(ctx)
		}
	}()
	if days := s.config.HTTPCaptureRetentionDays(); days > 0 {
		ids, err := s.storage.HTTPCaptureRunsEndedBefore(ctx, now.AddDate(0, 0, -days))
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("Deleting expired capture runs failed", "error", err)
			}
			return
		}
		for _, id := range ids {
			if !s.deleteCaptureRun(ctx, id) {
				return
			}
			deletedRuns++
		}
		if len(ids) > 0 {
			logger.Info("Deleted expired HTTP capture runs", "runs", len(ids), "days", days)
		}
	}
	limit := s.config.HTTPProxy.StorageLimit
	if limit <= 0 || s.captureBlobs == nil {
		return
	}
	size, err := s.captureBlobs.Size()
	if err != nil || size <= limit {
		return
	}
	runs, err := s.storage.HTTPCaptureRunsOldestFirst(ctx)
	if err != nil {
		return
	}
	deleted := 0
	for _, r := range runs {
		if size <= limit {
			break
		}
		if !s.deleteCaptureRun(ctx, r.RunID) {
			return
		}
		size -= r.Bytes
		deleted++
		deletedRuns++
	}
	if deleted > 0 {
		logger.Info("Deleted the oldest HTTP capture runs above the capture storage limit",
			"runs", deleted, "limit", diskspace.FormatBytes(limit))
	}
}

func (s *Server) deleteCaptureRun(ctx context.Context, id string) bool {
	if err := s.storage.DeleteHTTPCaptureRun(ctx, id); err != nil {
		if ctx.Err() == nil {
			logger.Error("Deleting a capture run failed", "run", id, "error", err)
		}
		return false
	}
	if s.captureBlobs != nil {
		if err := s.captureBlobs.RemoveRun(id); err != nil {
			logger.Warn("Removing a capture run's bodies failed", "run", id, "error", err)
		}
	}
	return true
}
