package sources

import (
	"crypto/sha256"
	"errors"
	"net/url"
	"regexp"
	"time"
)

const MinimumTokenLength = 16

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type Source struct {
	Slug      string
	Name      string
	TokenHash []byte
	Enabled   bool
	// StaleAfter is how long an alert from this source may go unreported before
	// it expires. It has to exceed the source Alertmanager's repeat_interval, so
	// it belongs to the source rather than to the server. Nil leaves the stored
	// value alone and falls back to the server default; zero disables expiry for
	// this source.
	StaleAfter *time.Duration
	// AlertmanagerURL is the base URL of the Alertmanager this source delivers
	// from, read to reconcile what promview holds against what still exists.
	// Nil leaves the stored value alone; an empty string disables reconciliation
	// for the source.
	AlertmanagerURL *string
	// AlertmanagerToken authenticates writes to that Alertmanager, sent as a
	// bearer credential when creating a silence. Reads work unauthenticated in
	// the deployments this targets; writes are the direction that is usually
	// protected. Nil leaves the stored value alone; an empty string clears it
	// and sends no credential.
	//
	// Unlike the ingestion token this is stored as given rather than hashed: it
	// has to be replayed to the Alertmanager, not compared against.
	AlertmanagerToken *string
}

// Patch carries the settings that can change after a source exists. Nil fields
// are left alone, and an empty value clears the setting. The token is
// deliberately absent: rotating a source's credentials is a different and far
// riskier operation than adjusting how it is read, and requiring one to do the
// other means an operator must handle a live secret to change a URL.
type Patch struct {
	Name              *string
	StaleAfter        *time.Duration
	AlertmanagerURL   *string
	AlertmanagerToken *string
}

// ValidatePatch rejects a change that would leave a source unusable.
func ValidatePatch(patch Patch) error {
	if patch.Name == nil && patch.StaleAfter == nil && patch.AlertmanagerURL == nil &&
		patch.AlertmanagerToken == nil {
		return errors.New("nothing to update")
	}
	if patch.Name != nil && *patch.Name == "" {
		return errors.New("source name is required")
	}
	if patch.StaleAfter != nil && *patch.StaleAfter < 0 {
		return errors.New("source stale-after must not be negative")
	}
	if patch.AlertmanagerURL != nil {
		return validateAlertmanagerURL(*patch.AlertmanagerURL)
	}
	return nil
}

// validateAlertmanagerURL accepts an empty value, which clears the setting and
// leaves the source to expiry alone.
func validateAlertmanagerURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("source alertmanager URL must be an absolute http or https URL")
	}
	return nil
}

func Validate(source Source, rawToken string) error {
	if !slugPattern.MatchString(source.Slug) {
		return errors.New("source slug must contain only lowercase letters, digits, underscores, or hyphens")
	}
	if source.Name == "" {
		return errors.New("source name is required")
	}
	if len(rawToken) < MinimumTokenLength {
		return errors.New("source token must contain at least 16 characters")
	}
	if source.StaleAfter != nil && *source.StaleAfter < 0 {
		return errors.New("source stale-after must not be negative")
	}
	if source.AlertmanagerURL != nil {
		if err := validateAlertmanagerURL(*source.AlertmanagerURL); err != nil {
			return err
		}
	}
	return nil
}

func HashToken(rawToken string) []byte {
	digest := sha256.Sum256([]byte(rawToken))
	return digest[:]
}
