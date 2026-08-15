package alerts

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("alert not found")

type Alert struct {
	ID           int64
	SourceSlug   string
	Fingerprint  string
	SourceStatus string
	Labels       map[string]string
	Annotations  map[string]string
	StartsAt     time.Time
	EndsAt       *time.Time
	GeneratorURL string
	ExternalURL  string
	FirstSeen    time.Time
	LastSeen     time.Time
	RepeatCount  int64
	Occurrence   int
	RawData      json.RawMessage
}

type Cursor struct {
	LastSeen time.Time `json:"lastSeen"`
	ID       int64     `json:"id"`
}

type Query struct {
	Limit    int
	Cursor   *Cursor
	Source   string
	Status   string
	Severity string
	Team     string
}

type ListResult struct {
	Alerts         []Alert
	NextCursor     *Cursor
	SeverityCounts map[string]int64
	Total          int64
	StreamCursor   int64
}

type StreamEvent struct {
	ID         int64     `json:"id"`
	Type       string    `json:"type"`
	AlertID    int64     `json:"-"`
	OccurredAt time.Time `json:"occurredAt"`
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
