package httpapi

import (
	"context"
	"crypto/subtle"
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
	"github.com/cropalato/promview/internal/config"
)

const maxWebhookBodyBytes = 4 << 20

type Store interface {
	Ingest(context.Context, []alertmanager.IncomingAlert) error
	ListAlerts(context.Context, alerts.Query) (alerts.ListResult, error)
	GetAlertDetail(context.Context, int64) (alerts.Detail, error)
	StreamEvents(context.Context, int64, int) ([]alerts.StreamEvent, error)
	Ping(context.Context) error
}

type API struct {
	config config.Config
	store  Store
}

func New(cfg config.Config, store Store) http.Handler {
	api := &API{config: cfg, store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", api.getConfig)
	mux.HandleFunc("GET /api/v1/alerts", api.listAlerts)
	mux.HandleFunc("GET /api/v1/alerts/{id}", api.getAlert)
	mux.HandleFunc("GET /api/v1/alerts/{id}/events", api.getAlertEvents)
	mux.HandleFunc("GET /api/v1/stream", api.streamAlerts)
	mux.HandleFunc("POST /api/v1/ingest/alertmanager/{source}", api.ingestAlertmanager)
	mux.HandleFunc("GET /health/live", api.live)
	mux.HandleFunc("GET /health/ready", api.ready)
	mux.Handle("GET /", spaHandler(cfg.WebDirectory))
	return mux
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
	detail, err := api.store.GetAlertDetail(r.Context(), id)
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
	result, err := api.store.ListAlerts(r.Context(), query)
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
	for {
		if err := r.Context().Err(); err != nil {
			return
		}
		events, err := api.store.StreamEvents(r.Context(), afterID, 100)
		if err != nil {
			return
		}
		for _, event := range events {
			payload := map[string]any{
				"id":         event.ID,
				"type":       event.Type,
				"alertId":    strconv.FormatInt(event.AlertID, 10),
				"occurredAt": event.OccurredAt,
			}
			data, err := json.Marshal(payload)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
			afterID = event.ID
		}
		if len(events) > 0 {
			flusher.Flush()
			lastKeepalive = time.Now()
			if len(events) == 100 {
				continue
			}
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

func (api *API) getConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"authMode":    api.config.AuthMode,
		"productName": "Promview",
	})
}

func (api *API) ingestAlertmanager(w http.ResponseWriter, r *http.Request) {
	if !api.authorized(r) {
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

func (api *API) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided := strings.TrimPrefix(header, prefix)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(api.config.IngestToken)) == 1
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
