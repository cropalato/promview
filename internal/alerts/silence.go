package alerts

import "errors"

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

// SilenceTarget is one Alertmanager a silence must reach, and the credential to
// reach it with.
type SilenceTarget struct {
	Source            string
	AlertmanagerURL   string
	AlertmanagerToken string
}

// SilenceScope is a resolved silence request: the exact label set to match, and
// every Alertmanager holding alerts that match it.
type SilenceScope struct {
	// Labels become one equality matcher each. For a single alert this is its
	// full label set; for a group it is the grouping key, minus `source`, which
	// names a promview source rather than an alert label and so selects the
	// target instead of constraining the match.
	Labels  map[string]string
	Targets []SilenceTarget
}

// SilenceResult reports what happened at one Alertmanager.
type SilenceResult struct {
	Source    string `json:"source"`
	SilenceID string `json:"silenceId,omitempty"`
	Error     string `json:"error,omitempty"`
}
