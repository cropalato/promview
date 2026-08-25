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
Reading the Alertmanager directly is what lets promview tell "this alert ended"
from "nobody told us anything". Webhook delivery cannot: Alertmanager suppresses
resolved notifications for silenced alerts, so an alert that clears inside a
maintenance window is never announced, and a delivery outage looks identical.

The API is read-only and unauthenticated in the deployments this targets, so the
client carries no credentials. A source that needs them will need this extended
rather than worked around.
*/

// LiveAlert is the slice of an Alertmanager alert reconciliation needs: what it
// is, and whether a silence or inhibition is currently holding it back.
type LiveAlert struct {
	Fingerprint string
	Suppressed  bool
	// SilencedBy holds the ids of the silences currently matching, empty when
	// none do. A suppressed alert with no silence ids is inhibited instead, and
	// the two are worth telling apart: an inhibition lifts itself when its
	// parent alert clears, while a silence has an author and an expiry somebody
	// chose and may want to revisit.
	SilencedBy []string
}

// Client reads the alerts an Alertmanager currently holds.
type Client struct {
	http *http.Client
}

func NewClient(timeout time.Duration) *Client {
	return &Client{http: &http.Client{Timeout: timeout}}
}

// LiveAlerts returns every alert the Alertmanager holds, including the silenced
// and inhibited ones. Suppressed alerts are asked for deliberately: they are
// still firing, and treating their absence from notifications as an ending is
// the bug this exists to prevent.
func (client *Client) LiveAlerts(ctx context.Context, baseURL string) ([]LiveAlert, error) {
	endpoint := strings.TrimSuffix(baseURL, "/") + "/api/v2/alerts?active=true&silenced=true&inhibited=true"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build alertmanager request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query alertmanager %s: %w", baseURL, err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alertmanager %s returned HTTP %d", baseURL, response.StatusCode)
	}

	var payload []struct {
		Fingerprint string `json:"fingerprint"`
		Status      struct {
			State      string   `json:"state"`
			SilencedBy []string `json:"silencedBy"`
		} `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode alertmanager response from %s: %w", baseURL, err)
	}

	live := make([]LiveAlert, 0, len(payload))
	for _, item := range payload {
		if item.Fingerprint == "" {
			// Without a fingerprint an alert cannot be matched to a stored one,
			// and guessing would risk resolving the wrong alert.
			return nil, errors.New("alertmanager returned an alert without a fingerprint")
		}
		silencedBy := item.Status.SilencedBy
		if silencedBy == nil {
			silencedBy = []string{}
		}
		live = append(live, LiveAlert{
			Fingerprint: item.Fingerprint,
			Suppressed:  item.Status.State == "suppressed",
			SilencedBy:  silencedBy,
		})
	}
	return live, nil
}
