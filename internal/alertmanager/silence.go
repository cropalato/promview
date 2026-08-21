package alertmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

/*
Creating a silence is the one place promview writes to an Alertmanager rather
than reading it. That asymmetry is deliberate everywhere else, so it is worth
being explicit about what this does: it hides alerts, on a system promview does
not own, on behalf of a named person. Every silence therefore carries who asked
for it and when it ends, and neither is optional.

Reads are unauthenticated in the deployments this targets. Writes commonly are
not, so a source may carry a bearer credential for them; an empty token sends no
header and matches the read path's posture.
*/

// Matcher is one label equality constraint. Alertmanager also supports negation
// and regular expressions; promview only ever silences an exact label set,
// because a matcher an operator did not read is a matcher that silences more
// than they meant.
type Matcher struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// IsRegex and IsEqual are sent explicitly rather than left to Alertmanager's
	// defaults, which have differed across versions.
	IsRegex bool `json:"isRegex"`
	IsEqual bool `json:"isEqual"`
}

// Silence is a request to suppress everything matching Matchers until EndsAt.
type Silence struct {
	Matchers  []Matcher
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	Comment   string
}

// MatchersFromLabels turns a label set into exact-match matchers, in a stable
// order so the same alert always produces the same silence body.
func MatchersFromLabels(labels map[string]string) []Matcher {
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	sort.Strings(names)
	matchers := make([]Matcher, 0, len(names))
	for _, name := range names {
		matchers = append(matchers, Matcher{Name: name, Value: labels[name], IsEqual: true})
	}
	return matchers
}

// CreateSilence posts a silence and returns the id Alertmanager assigned it.
func (client *Client) CreateSilence(
	ctx context.Context,
	baseURL string,
	token string,
	silence Silence,
) (string, error) {
	if err := validateSilence(silence); err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"matchers":  silence.Matchers,
		"startsAt":  silence.StartsAt.UTC().Format(time.RFC3339),
		"endsAt":    silence.EndsAt.UTC().Format(time.RFC3339),
		"createdBy": silence.CreatedBy,
		"comment":   silence.Comment,
	})
	if err != nil {
		return "", fmt.Errorf("encode silence: %w", err)
	}

	endpoint := strings.TrimSuffix(baseURL, "/") + "/api/v2/silences"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build alertmanager silence request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := client.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("create silence on alertmanager %s: %w", baseURL, err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("alertmanager %s returned HTTP %d creating a silence", baseURL, response.StatusCode)
	}

	var payload struct {
		SilenceID string `json:"silenceID"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode silence response from %s: %w", baseURL, err)
	}
	if payload.SilenceID == "" {
		// Without an id the caller cannot report or later expire the silence,
		// and reporting success for something unidentifiable is worse than
		// failing.
		return "", fmt.Errorf("alertmanager %s created a silence without an id", baseURL)
	}
	return payload.SilenceID, nil
}

func validateSilence(silence Silence) error {
	if len(silence.Matchers) == 0 {
		// A silence with no matchers matches every alert. Alertmanager rejects
		// this too, but not on every version, and the blast radius is the whole
		// deployment.
		return errors.New("a silence needs at least one matcher")
	}
	if silence.CreatedBy == "" {
		return errors.New("a silence needs an author")
	}
	if !silence.EndsAt.After(silence.StartsAt) {
		return errors.New("a silence must end after it starts")
	}
	return nil
}
