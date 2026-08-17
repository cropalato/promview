package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/config"
)

const maxWebhookBodyBytes = 4 << 20

type Store interface {
	Ingest(context.Context, []alertmanager.IncomingAlert) error
	AuthenticateSource(context.Context, string, string) (bool, error)
	ListAlerts(context.Context, auth.Principal, alerts.Query) (alerts.ListResult, error)
	GetAlertDetail(context.Context, auth.Principal, int64) (alerts.Detail, error)
	StreamEvents(context.Context, auth.Principal, int64, int) (alerts.StreamBatch, error)
	Ping(context.Context) error
}

type API struct {
	config        config.Config
	store         Store
	authenticator auth.Authenticator
}

func New(cfg config.Config, store Store, authenticator auth.Authenticator, authenticationHandlers ...http.Handler) http.Handler {
	api := &API{config: cfg, store: store, authenticator: authenticator}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", api.getConfig)
	mux.Handle("GET /api/v1/me", api.requireAuthentication(http.HandlerFunc(api.getMe)))
	mux.Handle("GET /api/v1/alerts", api.requireAuthentication(http.HandlerFunc(api.listAlerts)))
	mux.Handle("GET /api/v1/alerts/{id}", api.requireAuthentication(http.HandlerFunc(api.getAlert)))
	mux.Handle("GET /api/v1/alerts/{id}/events", api.requireAuthentication(http.HandlerFunc(api.getAlertEvents)))
	mux.Handle("GET /api/v1/stream", api.requireAuthentication(http.HandlerFunc(api.streamAlerts)))
	mux.HandleFunc("POST /api/v1/ingest/alertmanager/{source}", api.ingestAlertmanager)
	if len(authenticationHandlers) > 0 && authenticationHandlers[0] != nil {
		mux.Handle("GET /api/v1/auth/oidc/login", authenticationHandlers[0])
		mux.Handle("GET /api/v1/auth/oidc/callback", authenticationHandlers[0])
		mux.Handle("POST /api/v1/auth/logout", authenticationHandlers[0])
	}
	mux.HandleFunc("GET /health/live", api.live)
	mux.HandleFunc("GET /health/ready", api.ready)
	mux.Handle("GET /", spaHandler(cfg.WebDirectory))
	return mux
}

type principalContextKey struct{}

func (api *API) requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := api.authenticator.Authenticate(r.Context(), r)
		if err != nil {
			if errors.Is(err, auth.ErrUnauthenticated) {
				writeError(w, http.StatusUnauthorized, "authentication required")
			} else {
				writeError(w, http.StatusInternalServerError, "authentication failed")
			}
			return
		}
		if !principal.CanRead() {
			writeError(w, http.StatusForbidden, "read access denied")
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *API) getMe(w http.ResponseWriter, r *http.Request) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "principal is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, principal)
}

func (api *API) getAlert(w http.ResponseWriter, r *http.Request) {
	detail, ok := api.loadAlertDetail(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alert":   newAlertDetailResponse(detail.Alert),
		"history": detail.History,
	})
}

func (api *API) getAlertEvents(w http.ResponseWriter, r *http.Request) {
	detail, ok := api.loadAlertDetail(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": detail.History})
}

func (api *API) loadAlertDetail(w http.ResponseWriter, r *http.Request) (alerts.Detail, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "alert id is invalid")
		return alerts.Detail{}, false
	}
	principal, ok := requestPrincipal(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "principal is unavailable")
		return alerts.Detail{}, false
	}
	detail, err := api.store.GetAlertDetail(r.Context(), principal, id)
	if errors.Is(err, alerts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "alert not found")
		return alerts.Detail{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query alert")
		return alerts.Detail{}, false
	}
	return detail, true
}

func (api *API) listAlerts(w http.ResponseWriter, r *http.Request) {
	query, err := parseAlertQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	principal, ok := requestPrincipal(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "principal is unavailable")
		return
	}
	result, err := api.store.ListAlerts(r.Context(), principal, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query alerts")
		return
	}

	items := make([]alertResponse, 0, len(result.Alerts))
	for _, alert := range result.Alerts {
		items = append(items, newAlertResponse(alert))
	}
	nextCursor := ""
	if result.NextCursor != nil {
		nextCursor, err = encodeCursor(*result.NextCursor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode cursor")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alerts":         items,
		"nextCursor":     nextCursor,
		"severityCounts": result.SeverityCounts,
		"streamCursor":   result.StreamCursor,
		"total":          result.Total,
	})
}

func (api *API) streamAlerts(w http.ResponseWriter, r *http.Request) {
	afterID, err := streamCursor(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	lastKeepalive := time.Now()
	lastAuthentication := time.Now()
	principal, ok := requestPrincipal(r)
	if !ok {
		return
	}
	for {
		if err := r.Context().Err(); err != nil {
			return
		}
		if time.Since(lastAuthentication) >= 15*time.Second {
			principal, err = api.authenticator.Authenticate(r.Context(), r)
			if err != nil || !principal.CanRead() {
				return
			}
			lastAuthentication = time.Now()
		}
		batch, err := api.store.StreamEvents(r.Context(), principal, afterID, 100)
		if err != nil {
			return
		}
		for _, event := range batch.Events {
			payload := map[string]any{
				"id":         event.ID,
				"type":       event.Type,
				"alertId":    strconv.FormatInt(event.AlertID, 10),
				"occurredAt": event.OccurredAt,
			}
			if !event.Redacted {
				payload["severity"] = event.Severity
				payload["alertName"] = event.AlertName
				payload["summary"] = event.Summary
				payload["source"] = event.SourceSlug
				payload["team"] = event.Team
			}
			data, err := json.Marshal(payload)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
		}
		previousAfterID := afterID
		afterID = batch.ScannedThrough
		if len(batch.Events) > 0 {
			flusher.Flush()
			lastKeepalive = time.Now()
		}
		if afterID > previousAfterID {
			continue
		}
		if time.Since(lastKeepalive) >= 15*time.Second {
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			lastKeepalive = time.Now()
		}

		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func requestPrincipal(r *http.Request) (auth.Principal, bool) {
	principal, ok := r.Context().Value(principalContextKey{}).(auth.Principal)
	return principal, ok
}

func (api *API) getConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"authMode":    api.config.AuthMode,
		"productName": "Promview",
	})
}

func (api *API) ingestAlertmanager(w http.ResponseWriter, r *http.Request) {
	token := requestBearerToken(r)
	authorized, err := api.store.AuthenticateSource(r.Context(), r.PathValue("source"), token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to authenticate source")
		return
	}
	if !authorized {
		writeError(w, http.StatusUnauthorized, "invalid ingestion credentials")
		return
	}

	source := strings.TrimSpace(r.PathValue("source"))
	if source == "" {
		writeError(w, http.StatusBadRequest, "source is required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
	payload, err := alertmanager.Decode(json.NewDecoder(r.Body))
	if err != nil {
		status := http.StatusBadRequest
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, err.Error())
		return
	}

	alerts := alertmanager.Normalize(payload, source, time.Now())
	if err := api.store.Ingest(r.Context(), alerts); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist alerts")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": len(alerts)})
}

func (api *API) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type alertResponse struct {
	ID           string            `json:"id"`
	Fingerprint  string            `json:"fingerprint"`
	Source       string            `json:"source"`
	Status       string            `json:"status"`
	Severity     string            `json:"severity"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       *time.Time        `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	ExternalURL  string            `json:"externalURL"`
	FirstSeen    time.Time         `json:"firstSeen"`
	LastSeen     time.Time         `json:"lastSeen"`
	RepeatCount  int64             `json:"repeatCount"`
}

type alertDetailResponse struct {
	alertResponse
	Occurrence int             `json:"occurrence"`
	RawData    json.RawMessage `json:"rawData"`
}

func newAlertResponse(alert alerts.Alert) alertResponse {
	severity := alert.Labels["severity"]
	if severity == "" {
		severity = "warning"
	}
	return alertResponse{
		ID:           strconv.FormatInt(alert.ID, 10),
		Fingerprint:  alert.Fingerprint,
		Source:       alert.SourceSlug,
		Status:       alert.SourceStatus,
		Severity:     severity,
		Labels:       alert.Labels,
		Annotations:  alert.Annotations,
		StartsAt:     alert.StartsAt,
		EndsAt:       alert.EndsAt,
		GeneratorURL: alert.GeneratorURL,
		ExternalURL:  alert.ExternalURL,
		FirstSeen:    alert.FirstSeen,
		LastSeen:     alert.LastSeen,
		RepeatCount:  alert.RepeatCount,
	}
}

func newAlertDetailResponse(alert alerts.Alert) alertDetailResponse {
	return alertDetailResponse{
		alertResponse: newAlertResponse(alert),
		Occurrence:    alert.Occurrence,
		RawData:       alert.RawData,
	}
}

func parseAlertQuery(r *http.Request) (alerts.Query, error) {
	values := r.URL.Query()
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			return alerts.Query{}, errors.New("limit must be between 1 and 500")
		}
		limit = parsed
	}

	status := values.Get("status")
	if status != "" && status != "firing" && status != "resolved" {
		return alerts.Query{}, errors.New("status must be firing or resolved")
	}

	query := alerts.Query{
		Limit:    limit,
		Source:   strings.TrimSpace(values.Get("source")),
		Status:   status,
		Severity: strings.TrimSpace(values.Get("severity")),
		Team:     strings.TrimSpace(values.Get("team")),
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeCursor(raw)
		if err != nil {
			return alerts.Query{}, errors.New("cursor is invalid")
		}
		query.Cursor = &cursor
	}
	return query, nil
}

func encodeCursor(cursor alerts.Cursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(value string) (alerts.Cursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return alerts.Cursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	var cursor alerts.Cursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return alerts.Cursor{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	if cursor.ID < 1 || cursor.LastSeen.IsZero() {
		return alerts.Cursor{}, errors.New("cursor is incomplete")
	}
	return cursor, nil
}

func streamCursor(r *http.Request) (int64, error) {
	value := r.URL.Query().Get("cursor")
	if value == "" {
		value = r.Header.Get("Last-Event-ID")
	}
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, errors.New("stream cursor is invalid")
	}
	return cursor, nil
}

func (api *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := api.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func requestBearerToken(r *http.Request) string {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func spaHandler(directory string) http.Handler {
	files := http.FileServer(http.Dir(directory))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(directory, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(directory, "index.html"))
	})
}
