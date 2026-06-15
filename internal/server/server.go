package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/engine"
	"github.com/bluvenr/hookrun/internal/logger"
	"github.com/bluvenr/hookrun/internal/version"
)

// Server wraps the HTTP server and all dependencies.
type Server struct {
	httpServer *http.Server
	engine     *engine.Engine
	configMgr  *config.Manager
	logger     *logger.Logger
	startTime  time.Time
}

// New creates a new Server instance.
func New(configMgr *config.Manager, eng *engine.Engine, log *logger.Logger) *Server {
	return &Server{
		engine:    eng,
		configMgr: configMgr,
		logger:    log,
	}
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	cfg := s.configMgr.Global()
	route := cfg.Server.Route
	if route == "" {
		route = "/webhook"
	}

	mux := http.NewServeMux()

	// Webhook endpoints:
	//   /webhook          -> iterate all configs (if allow_all is true)
	//   /webhook/{name}   -> target specific config by filename
	mux.HandleFunc(route, s.handleWebhook)
	if !strings.HasSuffix(route, "/") {
		mux.HandleFunc(route+"/", s.handleWebhook)
	}

	// Health check endpoint
	mux.HandleFunc("/health", s.handleHealth)

	// Reload endpoint (internal use)
	mux.HandleFunc("/_reload", s.handleReload)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // longer for slow actions
		IdleTimeout:  60 * time.Second,
	}

	s.startTime = time.Now()
	s.logger.Info("HookRun server starting on %s (route: %s)", addr, route)
	s.logger.Info("Loaded %d rule config file(s)", s.configMgr.RuleCount())

	// Listen for shutdown signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or error
	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case sig := <-stop:
		s.logger.Info("Received signal %v, shutting down...", sig)
		return s.Shutdown()
	}
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s.logger.Info("Shutting down server...")
	s.engine.CloseRuleLoggers()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	s.logger.Info("Server stopped")
	s.logger.Close()
	return nil
}

// handleWebhook processes incoming webhook requests.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, engine.Response{
			Code:    405,
			Message: "Method not allowed",
		})
		return
	}

	cfg := s.configMgr.Global()
	baseRoute := cfg.Server.Route
	if baseRoute == "" {
		baseRoute = "/webhook"
	}

	// Extract target filename from URL path (e.g. /webhook/my-app -> "my-app")
	target := ""
	path := r.URL.Path
	if strings.HasPrefix(path, baseRoute+"/") {
		target = strings.TrimPrefix(path, baseRoute+"/")
		target = strings.TrimSuffix(target, "/") // remove trailing slash
	}

	s.logger.Info("Webhook request from %s (target: %s)", r.RemoteAddr, targetOrAll(target))

	// Apply request body size limit
	cfg2 := s.configMgr.Global()
	if cfg2.Server.MaxBodySizeMB != nil && *cfg2.Server.MaxBodySizeMB > 0 {
		maxBytes := int64(*cfg2.Server.MaxBodySizeMB) * 1024 * 1024
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	}

	// Parse request
	reqData, err := engine.ParseRequest(r)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.logger.Warn("Request body too large from %s (limit: %d bytes)", r.RemoteAddr, maxBytesErr.Limit)
			writeJSON(w, http.StatusRequestEntityTooLarge, engine.Response{
				Code:    413,
				Message: fmt.Sprintf("Request body too large (limit: %d MB)", *cfg2.Server.MaxBodySizeMB),
			})
			return
		}
		s.logger.Warn("Failed to read request body from %s: %v", r.RemoteAddr, err)
		writeJSON(w, http.StatusBadRequest, engine.Response{
			Code:    400,
			Message: fmt.Sprintf("Failed to read request body: %v", err),
		})
		return
	}

	var responses []engine.Response

	if target != "" {
		// Targeted mode: route to specific config file
		ruleConfig := s.configMgr.FindByFileName(target)
		if ruleConfig == nil {
			s.logger.Warn("Target config '%s' not found", target)
			writeJSON(w, http.StatusNotFound, engine.Response{
				Code:    404,
				Message: fmt.Sprintf("Config '%s' not found", target),
			})
			return
		}
		responses = s.engine.ProcessTargeted(ruleConfig, reqData)
	} else {
		// Base route: check if allow_all is enabled
		if !cfg.Server.IsAllowAll() {
			s.logger.Warn("Base route iteration is disabled (allow_all: false)")
			writeJSON(w, http.StatusBadRequest, engine.Response{
				Code:    400,
				Message: "Base route iteration is disabled, please specify target: /webhook/{name}",
			})
			return
		}
		// Choose processing mode
		responses = s.engine.Process(reqData) // first match wins
	}

	// Log results
	for _, resp := range responses {
		s.logger.Info("Response [%d] config=%s rule=%s: %s", resp.Code, resp.Config, resp.Rule, resp.Message)
	}

	// Determine overall HTTP status
	statusCode := http.StatusOK
	for _, resp := range responses {
		if resp.Code >= 400 {
			statusCode = resp.Code
			break
		}
	}

	// If single response, return it directly; otherwise return array
	if len(responses) == 1 {
		writeJSON(w, statusCode, responses[0])
	} else {
		writeJSON(w, statusCode, responses)
	}
}

// targetOrAll returns the target name or "all" for logging.
func targetOrAll(target string) string {
	if target == "" {
		return "all"
	}
	return target
}

// handleHealth returns server health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(s.startTime)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"uptime":  uptime.String(),
		"rules":   s.configMgr.RuleCount(),
		"version": version.Version,
	})
}

// handleReload triggers a hot-reload of all configs.
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, engine.Response{
			Code:    405,
			Message: "Method not allowed",
		})
		return
	}

	s.logger.Info("Hot-reload triggered via API")

	if err := s.configMgr.Reload(); err != nil {
		s.logger.Error("Reload failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, engine.Response{
			Code:    500,
			Message: fmt.Sprintf("Reload failed: %v", err),
		})
		return
	}

	// Update engine with new configs
	s.engine.CloseRuleLoggers() // close old rule-level loggers
	s.engine.UpdateConfigs(s.configMgr.Rules())

	s.logger.Info("Reload successful, loaded %d rule config(s)", s.configMgr.RuleCount())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok",
		"rules":  s.configMgr.RuleCount(),
	})
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
