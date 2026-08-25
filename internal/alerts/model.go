package alerts

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	Suppressed bool
	// SilencedBy names the silences currently matching. Suppressed with no ids
	// means inhibited, which the console renders differently: an inhibition
	// lifts itself, a silence was somebody's decision and has an expiry.
	SilencedBy     []string
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
	// Suppressed filters on whether a silence or inhibition is holding the
	// alert back. Nil leaves suppressed alerts in the list, which is the
	// default on purpose: hiding them by default is how an alert disappears
	// without anybody deciding it should.
	Suppressed *bool
	Matches    []LabelMatcher
	Sort       string
	Order      string
	// GroupBy collapses the result into one row per distinct combination of
	// these label keys, plus the special source key. Empty lists alerts individually.
	GroupBy     []string
	GroupCursor *GroupCursor
}

var groupLabelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// MaxGroupKeys bounds how finely a caller can slice the result; past a few keys
// grouping stops collapsing anything and only costs an aggregation.
const MaxGroupKeys = 3

// ValidateGroupBy reports whether these keys can be grouped on. Source is a
// special stored field; every other key is a canonical alert label name.
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
	return name == "source" || groupLabelNamePattern.MatchString(name)
}

// Group is one collapsed row: every alert sharing the same values for the
// grouping keys, summarised. Counts are computed under the same filters and
// read restrictions as the alerts themselves, so a group never reports members
// the reader cannot open.
type Group struct {
	Key          map[string]string
	Total        int64
	Acknowledged int64
	// Silenced is how many members a silence or inhibition is holding back. A
	// group is otherwise indistinguishable from a fully firing one, which is
	// the case an operator most needs to tell apart at a glance.
	Silenced         int64
	SeverityCounts   map[string]int64
	WorstSeverity    string
	LatestLastSeen   time.Time
	EarliestStartsAt time.Time
	// SampleAlertID is the most recently seen member. A group of one is
	// rendered as a plain row, and this is the alert it opens.
	SampleAlertID int64
}

// GroupCursor pages through groups. Value is the deterministic aggregate used
// for an explicitly requested sort; the legacy default uses severity and
// recency. Sort and Order bind a cursor to its ordering.
type GroupCursor struct {
	SeverityRank   int       `json:"severityRank"`
	LatestLastSeen time.Time `json:"latestLastSeen"`
	Key            []string  `json:"key"`
	Sort           string    `json:"sort"`
	Order          string    `json:"order"`
	Value          string    `json:"value"`
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
		Source     string         `json:"source"`
		Status     string         `json:"status"`
		Severity   string         `json:"severity"`
		Team       string         `json:"team"`
		Suppressed *bool          `json:"suppressed"`
		Matches    []LabelMatcher `json:"matches"`
		GroupBy    []string       `json:"groupBy"`
		Sort       string         `json:"sort"`
		Order      string         `json:"order"`
	}{query.Source, query.Status, query.Severity, query.Team, query.Suppressed, query.Matches, query.GroupBy, query.Sort, query.Order})
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
	// Silences are the promview-created silences currently holding this alert
	// back, matched by the ids the source Alertmanager reports. Empty when the
	// alert is suppressed by a silence somebody made elsewhere, or by an
	// inhibition, and the console says so rather than inventing an author.
	Silences []SilenceRecord
}
