package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bluvenr/hookrun/internal/engine"
)

// registerRequest is the JSON body for POST /api/relay/register.
type registerRequest struct {
	URL   string   `json:"url"`
	Token string   `json:"token,omitempty"`
	Tags  []string `json:"tags"`
	TTL   int      `json:"ttl,omitempty"`
}

// unregisterRequest is the JSON body for DELETE /api/relay/register.
type unregisterRequest struct {
	URL string `json:"url"`
}

// targetResponse is the JSON response for a single registered target.
type targetResponse struct {
	URL       string   `json:"url"`
	Tags      []string `json:"tags"`
	TTL       int      `json:"ttl"`
	LastSeen  string   `json:"last_seen"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

// targetsResponse is the JSON response for GET /api/relay/targets.
type targetsResponse struct {
	Targets []targetResponse `json:"targets"`
	Total   int              `json:"total"`
}

// handleRelayRegister handles POST /api/relay/register.
func (s *Server) handleRelayRegister(w http.ResponseWriter, r *http.Request) {
	registry := s.engine.Registry()
	if registry == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "relay registry is not enabled",
		})
		return
	}

	// Auth check
	if !s.checkRegistryAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	switch r.Method {
	case http.MethodPost:
		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid JSON body: " + err.Error(),
			})
			return
		}

		if req.URL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "'url' is required",
			})
			return
		}
		if len(req.Tags) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "'tags' must not be empty",
			})
			return
		}

		entry := engine.RegistryEntry{
			URL:   req.URL,
			Token: req.Token,
			Tags:  req.Tags,
			TTL:   req.TTL,
		}

		if err := registry.Register(entry); err != nil {
			if errors.Is(err, engine.ErrRegistryFull) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{
					"error": "registry is full",
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":        "registered",
			"targets_count": registry.Count(),
		})

	case http.MethodDelete:
		var req unregisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid JSON body: " + err.Error(),
			})
			return
		}

		if req.URL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "'url' is required",
			})
			return
		}

		registry.Unregister(req.URL)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":        "unregistered",
			"targets_count": registry.Count(),
		})

	default:
		w.Header().Set("Allow", "POST, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
	}
}

// handleRelayTargets handles GET /api/relay/targets.
func (s *Server) handleRelayTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	registry := s.engine.Registry()
	if registry == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "relay registry is not enabled",
		})
		return
	}

	// Auth check
	if !s.checkRegistryAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	entries := registry.List()
	targets := make([]targetResponse, 0, len(entries))
	for _, e := range entries {
		tr := targetResponse{
			URL:      e.URL,
			Tags:     e.Tags,
			TTL:      e.TTL,
			LastSeen: e.LastSeen.Format("2006-01-02T15:04:05Z"),
		}
		if e.TTL > 0 {
			tr.ExpiresAt = e.LastSeen.Add(
				durationSeconds(e.TTL),
			).Format("2006-01-02T15:04:05Z")
		}
		targets = append(targets, tr)
	}

	writeJSON(w, http.StatusOK, targetsResponse{
		Targets: targets,
		Total:   len(targets),
	})
}

// checkRegistryAuth verifies the Bearer token against the configured registry token.
func (s *Server) checkRegistryAuth(r *http.Request) bool {
	cfg := s.configMgr.Global()
	expectedToken := cfg.Server.RelayRegistryToken

	// If no token is configured, auth is not required
	if expectedToken == "" {
		return true
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}

	// Support "Bearer <token>" format
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ") == expectedToken
	}

	return authHeader == expectedToken
}

// durationSeconds converts seconds to time.Duration.
func durationSeconds(s int) time.Duration {
	return time.Duration(s) * time.Second
}
