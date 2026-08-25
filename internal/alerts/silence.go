package alerts

import (
	"errors"
	"time"
)

/*
What a silence needs to know before it can be created: which labels it matches
on, and which Alertmanagers have to hear about it.

Those are two different questions. A group keyed only by alertname can hold
alerts delivered by several sources, each with its own Alertmanager, and
silencing one of them would leave the rest firing while the console reported
the group handled.
*/

// ErrNoSilenceTarget is returned when nothing in scope has an Alertmanager to
// write to. It is a configuration gap rather than a failure: a source with no
// alertmanager_url is not reconciled either.
var ErrNoSilenceTarget = errors.New("no source in scope has an alertmanager url")

// SilenceTarget is one Alertmanager a silence must reach, the credential to
// reach it with, and the exact match to write there.
type SilenceTarget struct {
	Source            string
	AlertmanagerURL   string
	AlertmanagerToken string
	// Labels become one equality matcher each: the narrowest equality match
	// that still covers every in-scope member this Alertmanager holds.
	//
	// It is per target rather than per request because a group spanning two
	// Alertmanagers usually differs between them on exactly the label worth
	// matching on — members at one may all carry cluster=a and at the other
	// cluster=b. A single shared match would drop `cluster` and silence both
	// clusters at both places.
	Labels map[string]string
	// Members is how many in-scope alerts this target contributed, so a caller
	// can report what a silence is actually covering.
	Members int
}

// SilenceScope is a resolved silence request: every Alertmanager to write to,
// and what to write there.
type SilenceScope struct {
	// Labels is what every target agrees on, and so a subset of each target's
	// own Labels. It is what a console previews when it has one line to show;
	// the silence actually written is always at least this narrow.
	Labels  map[string]string
	Targets []SilenceTarget
}

// CommonLabels folds the targets down to the labels they all share, which is
// the honest single-line summary of a scope that writes several matches.
func CommonLabels(targets []SilenceTarget) map[string]string {
	common := map[string]string{}
	for index, target := range targets {
		if index == 0 {
			for name, value := range target.Labels {
				common[name] = value
			}
			continue
		}
		for name, value := range common {
			if other, ok := target.Labels[name]; !ok || other != value {
				delete(common, name)
			}
		}
	}
	return common
}

// SilenceResult reports what happened at one Alertmanager, including the exact
// match written there. The match is per result because it is per target, and an
// operator reading a partial failure needs to know what did land, not only
// where.
type SilenceResult struct {
	Source    string            `json:"source"`
	SilenceID string            `json:"silenceId,omitempty"`
	Matchers  map[string]string `json:"matchers"`
	Members   int               `json:"members"`
	Error     string            `json:"error,omitempty"`
}

// SilenceRecord is a silence promview created itself, kept after Alertmanager
// has expired and forgotten it. Alertmanager keeps the live state; this keeps
// the reasoning, which is what lets the console still say who silenced an alert
// and why once the silence is gone.
type SilenceRecord struct {
	Source    string            `json:"source"`
	SilenceID string            `json:"silenceId"`
	Matchers  map[string]string `json:"matchers"`
	CreatedBy string            `json:"createdBy"`
	Comment   string            `json:"comment"`
	StartsAt  time.Time         `json:"startsAt"`
	EndsAt    time.Time         `json:"endsAt"`
}
