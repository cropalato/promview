package alerts

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("alert not found")

// Source status values. Firing and resolved are reported by the source itself;
// expired is the console's own conclusion after the source went quiet for longer
// than its configured window, which is not the same claim as resolved.
const (
	StatusFiring   = "firing"
	StatusResolved = "resolved"
	StatusExpired  = "expired"
)

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
	Acknowledged bool
	// Suppressed means a silence or inhibition is holding the alert back at the
	// source. It is separate from status because such an alert is still firing.
	Suppressed     bool
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
	// GroupBy collapses the result into one row per distinct combination of
	// these label keys. Empty lists alerts individually.
	GroupBy     []string
	GroupCursor *GroupCursor
}

// GroupKeys is the vocabulary a caller may group by, shared by the HTTP layer
// and the store so a request is rejected before it reaches SQL. It is closed
// rather than any label because each key becomes a GROUP BY expression, and an
// unbounded-cardinality label produces one group per alert.
var GroupKeys = []string{"alertname", "source", "team", "severity", "instance"}

// MaxGroupKeys bounds how finely a caller can slice the result; past a few keys
// grouping stops collapsing anything and only costs an aggregation.
const MaxGroupKeys = 3

// ValidateGroupBy reports whether these keys can be grouped on.
func ValidateGroupBy(groupBy []string) error {
	if len(groupBy) == 0 {
		return errors.New("group by requires at least one key")
	}
	if len(groupBy) > MaxGroupKeys {
		return fmt.Errorf("group by accepts at most %d keys", MaxGroupKeys)
	}
	seen := make(map[string]bool, len(groupBy))
	for _, key := range groupBy {
		if !IsGroupKey(key) {
			return fmt.Errorf("cannot group by %q", key)
		}
		if seen[key] {
			return fmt.Errorf("duplicate group key %q", key)
		}
		seen[key] = true
	}
	return nil
}

func IsGroupKey(name string) bool {
	for _, key := range GroupKeys {
		if key == name {
			return true
		}
	}
	return false
}

// Group is one collapsed row: every alert sharing the same values for the
// grouping keys, summarised. Counts are computed under the same filters and
// read restrictions as the alerts themselves, so a group never reports members
// the reader cannot open.
type Group struct {
	Key              map[string]string
	Total            int64
	Acknowledged     int64
	SeverityCounts   map[string]int64
	WorstSeverity    string
	LatestLastSeen   time.Time
	EarliestStartsAt time.Time
	// SampleAlertID is the most recently seen member. A group of one is
	// rendered as a plain row, and this is the alert it opens.
	SampleAlertID int64
}

// GroupCursor pages through groups. Groups are ordered by worst severity then
// recency, both descending, so the cursor carries those two values plus the key
// itself to break ties.
type GroupCursor struct {
	SeverityRank   int       `json:"severityRank"`
	LatestLastSeen time.Time `json:"latestLastSeen"`
	Key            []string  `json:"key"`
	Query          string    `json:"query"`
}

type GroupResult struct {
	Groups         []Group
	NextCursor     *GroupCursor
	TotalGroups    int64
	TotalAlerts    int64
	SeverityCounts map[string]int64
	StreamCursor   int64
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
		GroupBy  []string       `json:"groupBy"`
	}{query.Source, query.Status, query.Severity, query.Team, query.Matches, query.GroupBy})
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
