package sources

import (
	"crypto/sha256"
	"errors"
	"regexp"
)

const MinimumTokenLength = 16

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type Source struct {
	Slug      string
	Name      string
	TokenHash []byte
	Enabled   bool
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
	return nil
}

func HashToken(rawToken string) []byte {
	digest := sha256.Sum256([]byte(rawToken))
	return digest[:]
}
