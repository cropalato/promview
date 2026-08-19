package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/config"
	"github.com/cropalato/promview/internal/preferences"
)

const maxWebhookBodyBytes = 4 << 20

var labelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type Store interface {
	Ingest(context.Context, []alertmanager.IncomingAlert) error
	AuthenticateSource(context.Context, string, string) (bool, error)
	ListAlerts(context.Context, auth.Principal, alerts.Query) (alerts.ListResult, error)
	GroupAlerts(context.Context, auth.Principal, alerts.Query) (alerts.GroupResult, error)
	GetAlertDetail(context.Context, auth.Principal, int64) (alerts.Detail, error)
	AcknowledgeAlert(context.Context, auth.Principal, int64, bool) (alerts.Detail, error)
	StreamEvents(context.Context, auth.Principal, int64, int) (alerts.StreamBatch, error)
	ReadPreferences(context.Context, auth.Principal) (preferences.Preferences, error)
	WritePreferences(context.Context, auth.Principal, preferences.Preferences) error
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
	mux.Handle("POST /api/v1/alerts/{id}/acknowledge", api.requireAuthentication(http.HandlerFunc(api.acknowledgeAlert)))
	mux.Handle("GET /api/v1/stream", api.requireAuthentication(http.HandlerFunc(api.streamAlerts)))
	mux.Handle("GET /api/v1/preferences", api.requireAuthentication(http.HandlerFunc(api.getPreferences)))
	mux.Handle("PUT /api/v1/preferences", api.requireAuthentication(http.HandlerFunc(api.putPreferences)))
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
	principal, ok := requestPrincipal(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "principal is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alert":   newAlertDetailResponse(detail.Alert, auth.CanOperateLabels(principal, detail.Alert.Labels)),
		"history": detail.History,
	})
}

func (api *API) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "principal is unavailable")
		return
	}
	if !principal.CanOperate() {
		writeError(w, http.StatusForbidden, "operator access required")
		return
	}
	if !validMutationOrigin(r) {
		writeError(w, http.StatusForbidden, "invalid request origin")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "alert id is invalid")
		return
	}
	var body struct {
		Acknowledged *bool `json:"acknowledged"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Acknowledged == nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "acknowledged must be a boolean")
		return
	}
	detail, err := api.store.AcknowledgeAlert(r.Context(), principal, id, *body.Acknowledged)
	if errors.Is(err, alerts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update acknowledgement")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alert":   newAlertDetailResponse(detail.Alert, auth.CanOperateLabels(principal, detail.Alert.Labels)),
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
	if len(query.GroupBy) > 0 {
		api.listAlertGroups(w, r, principal, query)
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

// listAlertGroups answers the grouped shape of GET /api/v1/alerts. Expanding a
// group needs no endpoint of its own: the console re-queries this same handler
// with an equality matcher on the group's key, which keeps cursoring, sorting
// and read restrictions identical between a group's members and the flat list.
func (api *API) listAlertGroups(w http.ResponseWriter, r *http.Request, principal auth.Principal, query alerts.Query) {
	result, err := api.store.GroupAlerts(r.Context(), principal, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to group alerts")
		return
	}
	groups := make([]alertGroupResponse, 0, len(result.Groups))
	for _, group := range result.Groups {
		groups = append(groups, alertGroupResponse{
			Key:              group.Key,
			Total:            group.Total,
			Acknowledged:     group.Acknowledged,
			SeverityCounts:   group.SeverityCounts,
			WorstSeverity:    group.WorstSeverity,
			LatestLastSeen:   group.LatestLastSeen,
			EarliestStartsAt: group.EarliestStartsAt,
			SampleAlertID:    strconv.FormatInt(group.SampleAlertID, 10),
		})
	}
	nextCursor := ""
	if result.NextCursor != nil {
		nextCursor, err = encodeGroupCursor(*result.NextCursor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode cursor")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"groups":         groups,
		"nextCursor":     nextCursor,
		"severityCounts": result.SeverityCounts,
		"streamCursor":   result.StreamCursor,
		"total":          result.TotalAlerts,
		"totalGroups":    result.TotalGroups,
	})
}

// getPreferences returns the caller's console layout. In open mode there is no
// user to key against, so it answers 404 and the console falls back to storing
// layout in the browser. That is a statement about identity, not permission,
// which is why it is not a 401.
func (api *API) getPreferences(w http.ResponseWriter, r *http.Request) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "principal is unavailable")
		return
	}
	stored, err := api.store.ReadPreferences(r.Context(), principal)
	if errors.Is(err, preferences.ErrNoSubject) {
		writeError(w, http.StatusNotFound, "preferences are unavailable without a signed-in user")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read preferences")
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

func (api *API) putPreferences(w http.ResponseWriter, r *http.Request) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "principal is unavailable")
		return
	}
	var value preferences.Preferences
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		writeError(w, http.StatusBadRequest, "preferences payload is invalid")
		return
	}
	if err := preferences.Validate(value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := api.store.WritePreferences(r.Context(), principal, value); err != nil {
		if errors.Is(err, preferences.ErrNoSubject) {
			writeError(w, http.StatusNotFound, "preferences are unavailable without a signed-in user")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save preferences")
		return
	}
	writeJSON(w, http.StatusOK, value)
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
	// Suppressed is on the summary rather than only the detail: an operator
	// scanning the list needs to see that an alert is inside a maintenance
	// window without opening it.
	Suppressed bool `json:"suppressed"`
}

type alertDetailResponse struct {
	alertResponse
	Occurrence     int             `json:"occurrence"`
	Acknowledged   bool            `json:"acknowledged"`
	AcknowledgedAt *time.Time      `json:"acknowledgedAt"`
	AcknowledgedBy string          `json:"acknowledgedBy"`
	Actions        alertActions    `json:"actions"`
	RawData        json.RawMessage `json:"rawData"`
}

type alertActions struct {
	CanAcknowledge bool `json:"canAcknowledge"`
}

// alertGroupResponse is one collapsed row. SampleAlertID is a string like every
// other id the API emits, so the console never has to care that ids are numeric
// on the server.
type alertGroupResponse struct {
	Key              map[string]string `json:"key"`
	Total            int64             `json:"total"`
	Acknowledged     int64             `json:"acknowledged"`
	SeverityCounts   map[string]int64  `json:"severityCounts"`
	WorstSeverity    string            `json:"worstSeverity"`
	LatestLastSeen   time.Time         `json:"latestLastSeen"`
	EarliestStartsAt time.Time         `json:"earliestStartsAt"`
	SampleAlertID    string            `json:"sampleAlertId"`
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
		Suppressed:   alert.Suppressed,
	}
}

func newAlertDetailResponse(alert alerts.Alert, canAcknowledge bool) alertDetailResponse {
	return alertDetailResponse{
		alertResponse:  newAlertResponse(alert),
		Occurrence:     alert.Occurrence,
		Acknowledged:   alert.Acknowledged,
		AcknowledgedAt: alert.AcknowledgedAt,
		AcknowledgedBy: alert.AcknowledgedBy,
		Actions:        alertActions{CanAcknowledge: canAcknowledge},
		RawData:        alert.RawData,
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
	switch status {
	case "", alerts.StatusFiring, alerts.StatusResolved, alerts.StatusExpired:
	default:
		return alerts.Query{}, errors.New("status must be firing, resolved, or expired")
	}

	query := alerts.Query{
		Limit:    limit,
		Source:   strings.TrimSpace(values.Get("source")),
		Status:   status,
		Severity: strings.TrimSpace(values.Get("severity")),
		Team:     strings.TrimSpace(values.Get("team")),
		Sort:     values.Get("sort"),
		Order:    values.Get("order"),
	}
	if query.Sort == "" {
		query.Sort = alerts.DefaultSort
	}
	if !isAlertSort(query.Sort) {
		return alerts.Query{}, errors.New("sort is invalid")
	}
	if query.Order == "" {
		query.Order = alerts.DefaultOrder
	}
	if query.Order != "asc" && query.Order != "desc" {
		return alerts.Query{}, errors.New("order must be asc or desc")
	}
	if raw := strings.TrimSpace(values.Get("groupBy")); raw != "" {
		for _, key := range strings.Split(raw, ",") {
			query.GroupBy = append(query.GroupBy, strings.TrimSpace(key))
		}
		if err := alerts.ValidateGroupBy(query.GroupBy); err != nil {
			return alerts.Query{}, err
		}
	}
	for _, raw := range values["match"] {
		matcher, err := parseLabelMatcher(raw)
		if err != nil {
			return alerts.Query{}, err
		}
		query.Matches = append(query.Matches, matcher)
	}
	if raw := values.Get("cursor"); raw != "" {
		if len(query.GroupBy) > 0 {
			cursor, err := decodeGroupCursor(raw)
			if err != nil {
				return alerts.Query{}, errors.New("cursor is invalid")
			}
			if cursor.Query != query.CursorIdentity() || len(cursor.Key) != len(query.GroupBy) {
				return alerts.Query{}, errors.New("cursor does not match query")
			}
			query.GroupCursor = &cursor
			return query, nil
		}
		cursor, err := decodeCursor(raw)
		if err != nil {
			return alerts.Query{}, errors.New("cursor is invalid")
		}
		if cursor.Sort != query.Sort || cursor.Order != query.Order || cursor.Query != query.CursorIdentity() {
			return alerts.Query{}, errors.New("cursor does not match query")
		}
		if !validCursorValue(cursor) {
			return alerts.Query{}, errors.New("cursor is invalid")
		}
		query.Cursor = &cursor
	}
	return query, nil
}

func encodeGroupCursor(cursor alerts.GroupCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal group cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeGroupCursor(value string) (alerts.GroupCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return alerts.GroupCursor{}, fmt.Errorf("decode group cursor: %w", err)
	}
	var cursor alerts.GroupCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return alerts.GroupCursor{}, fmt.Errorf("decode group cursor: %w", err)
	}
	return cursor, nil
}

func parseLabelMatcher(raw string) (alerts.LabelMatcher, error) {
	if strings.Contains(raw, "=~") || strings.Contains(raw, "!~") {
		return alerts.LabelMatcher{}, errors.New("match supports only = and !=")
	}
	operatorAt := strings.Index(raw, "!=")
	operator := "!="
	if operatorAt < 0 {
		operatorAt = strings.IndexByte(raw, '=')
		operator = "="
	}
	if operatorAt < 1 {
		return alerts.LabelMatcher{}, errors.New("match must be label=value or label!=value")
	}
	name := raw[:operatorAt]
	if !labelNamePattern.MatchString(name) {
		return alerts.LabelMatcher{}, errors.New("match label is invalid")
	}
	return alerts.LabelMatcher{Name: name, Operator: operator, Value: raw[operatorAt+len(operator):]}, nil
}

func isAlertSort(value string) bool {
	switch value {
	case "lastSeen", "startsAt", "severity", "alertname", "summary", "status", "team", "instance", "source":
		return true
	default:
		return false
	}
}

func validCursorValue(cursor alerts.Cursor) bool {
	switch cursor.Sort {
	case "lastSeen", "startsAt":
		_, err := time.Parse(time.RFC3339Nano, cursor.Value)
		return err == nil
	case "severity":
		value, err := strconv.Atoi(cursor.Value)
		return err == nil && value >= 0 && value <= 3
	default:
		return true
	}
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
	if cursor.ID < 1 || cursor.LastSeen.IsZero() || cursor.Sort == "" || cursor.Order == "" || cursor.Query == "" {
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

func validMutationOrigin(r *http.Request) bool {
	// Bearer credentials are not sent automatically by browsers, so this keeps
	// non-browser and future bearer clients independent of browser CSRF rules.
	if requestBearerToken(r) != "" {
		return true
	}
	if _, err := r.Cookie(auth.SessionCookieName); err != nil {
		return true
	}
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.Scheme != "" && origin.Host == r.Host
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
