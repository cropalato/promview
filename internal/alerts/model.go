package alerts

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("alert not found")

type Alert struct {
	ID             int64
	SourceSlug     string
	Fingerprint    string
	SourceStatus   string
	Labels         map[string]string
	Annotations    map[string]string
	StartsAt       time.Time
	EndsAt         *time.Time
	GeneratorURL   string
	ExternalURL    string
	FirstSeen      time.Time
	LastSeen       time.Time
	RepeatCount    int64
	Occurrence     int
	Acknowledged   bool
	AcknowledgedAt *time.Time
	AcknowledgedBy string
	RawData        json.RawMessage
}

type Cursor struct {
	LastSeen time.Time `json:"lastSeen"`
	ID       int64     `json:"id"`
	Sort     string    `json:"sort"`
	Order    string    `json:"order"`
	Query    string    `json:"query"`
	Value    string    `json:"value"`
}

type LabelMatcher struct {
	Name     string `json:"name"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type Query struct {
	Limit    int
	Cursor   *Cursor
	Source   string
	Status   string
	Severity string
	Team     string
	Matches  []LabelMatcher
	Sort     string
	Order    string
}

const (
	DefaultSort  = "lastSeen"
	DefaultOrder = "desc"
)

// CursorIdentity binds a cursor to the filters that produced it.
func (query Query) CursorIdentity() string {
	payload, err := json.Marshal(struct {
		Source   string         `json:"source"`
		Status   string         `json:"status"`
		Severity string         `json:"severity"`
		Team     string         `json:"team"`
		Matches  []LabelMatcher `json:"matches"`
	}{query.Source, query.Status, query.Severity, query.Team, query.Matches})
	if err != nil {
		panic(fmt.Sprintf("marshal cursor identity: %v", err))
	}
	return fmt.Sprintf("%x", sha256.Sum256(payload))
}

type ListResult struct {
	Alerts         []Alert
	NextCursor     *Cursor
	SeverityCounts map[string]int64
	Total          int64
	StreamCursor   int64
}

type StreamEvent struct {
	ID             int64             `json:"id"`
	Type           string            `json:"type"`
	AlertID        int64             `json:"-"`
	OccurredAt     time.Time         `json:"occurredAt"`
	Severity       string            `json:"severity"`
	AlertName      string            `json:"alertName"`
	Summary        string            `json:"summary"`
	SourceSlug     string            `json:"source"`
	Team           string            `json:"team"`
	Labels         map[string]string `json:"-"`
	PreviousLabels map[string]string `json:"-"`
	Redacted       bool              `json:"-"`
}

type StreamBatch struct {
	Events         []StreamEvent
	ScannedThrough int64
}

type HistoryEvent struct {
	ID           int64     `json:"id"`
	Occurrence   int       `json:"occurrence"`
	Type         string    `json:"type"`
	SourceStatus string    `json:"sourceStatus"`
	Actor        string    `json:"actor"`
	Message      string    `json:"message"`
	OccurredAt   time.Time `json:"occurredAt"`
}

type Detail struct {
	Alert   Alert
	History []HistoryEvent
}
