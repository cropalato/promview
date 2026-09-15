package alertmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

/*
Reading the silences an Alertmanager holds is what lets promview notice one
ending — or being deleted — without waiting for its recorded expiry. The alert
list cannot say this: an alert that cleared while silenced simply disappears,
and its silence going with it looks identical to a delivery gap.

The list is also stronger evidence than the alert list. Alertmanager persists
silences across restarts and alerts not at all, so "no silences" from a source
that just restarted is the truth, where "no alerts" is usually the restart.

Every silence comes back, matched to alerts or not. A preventive silence
written ahead of a maintenance window matches nothing yet, and one whose alerts
have already cleared matches nothing anymore; both are real and both belong in
the inventory.
*/

// ListedSilence is one silence as the Alertmanager reports it, in whatever
// state it is in.
type ListedSilence struct {
	ID string
	// State is Alertmanager's own word: active, pending (starts in the future),
	// or expired (ran out, or was deleted — Alertmanager reports both the same
	// way). Kept verbatim so an unknown state from a newer version is stored
	// rather than mistranslated.
	State string
	// Matchers are kept exactly as written, including the regex and negation
	// forms promview itself never writes: a silence made straight on the
	// Alertmanager is just as real, and flattening its match would misreport
	// what it suppresses.
	Matchers  []Matcher
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	Comment   string
}

// SilenceStateActive is the one state that actually suppresses; pending and
// expired silences hold nothing back.
const SilenceStateActive = "active"

// ListSilences returns every silence the Alertmanager currently reports,
// expired ones included. Expired silences are asked for deliberately: an alert
// can still name one in its silencedBy for a moment after it lapses, and the
// record explains what was holding the alert back.
func (client *Client) ListSilences(ctx context.Context, baseURL string) ([]ListedSilence, error) {
	endpoint := strings.TrimSuffix(baseURL, "/") + "/api/v2/silences"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build alertmanager silences request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query alertmanager silences %s: %w", baseURL, err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alertmanager %s returned HTTP %d listing silences", baseURL, response.StatusCode)
	}

	var payload []struct {
		ID     string `json:"id"`
		Status struct {
			State string `json:"state"`
		} `json:"status"`
		Matchers []struct {
			Name    string `json:"name"`
			Value   string `json:"value"`
			IsRegex bool   `json:"isRegex"`
			// A pointer because absence means equality: the field only exists
			// since Alertmanager 0.22, and an older payload without it is all
			// plain equality matchers.
			IsEqual *bool `json:"isEqual"`
		} `json:"matchers"`
		StartsAt  time.Time `json:"startsAt"`
		EndsAt    time.Time `json:"endsAt"`
		CreatedBy string    `json:"createdBy"`
		Comment   string    `json:"comment"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode alertmanager silences from %s: %w", baseURL, err)
	}

	silences := make([]ListedSilence, 0, len(payload))
	for _, item := range payload {
		if item.ID == "" {
			// Without an id a silence cannot be matched to an alert's silencedBy
			// or to a stored record, and guessing would corrupt the inventory.
			return nil, errors.New("alertmanager returned a silence without an id")
		}
		matchers := make([]Matcher, 0, len(item.Matchers))
		for _, matcher := range item.Matchers {
			isEqual := true
			if matcher.IsEqual != nil {
				isEqual = *matcher.IsEqual
			}
			matchers = append(matchers, Matcher{
				Name:    matcher.Name,
				Value:   matcher.Value,
				IsRegex: matcher.IsRegex,
				IsEqual: isEqual,
			})
		}
		silences = append(silences, ListedSilence{
			ID:        item.ID,
			State:     item.Status.State,
			Matchers:  matchers,
			StartsAt:  item.StartsAt,
			EndsAt:    item.EndsAt,
			CreatedBy: item.CreatedBy,
			Comment:   item.Comment,
		})
	}
	return silences, nil
}

// ActiveSilenceIDs folds a listing down to the ids that are suppressing right
// now, in the shape the reconciliation release check wants. Never nil for a
// successful listing: an Alertmanager with no active silences is a real answer,
// and nil is reserved for "the listing failed".
func ActiveSilenceIDs(silences []ListedSilence) map[string]bool {
	active := make(map[string]bool, len(silences))
	for _, silence := range silences {
		if silence.State == SilenceStateActive {
			active[silence.ID] = true
		}
	}
	return active
}
