package alertmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"sort"
	"strings"
	"time"
)

type Payload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

type IncomingAlert struct {
	SourceSlug   string
	Fingerprint  string
	Status       string
	Labels       map[string]string
	Annotations  map[string]string
	StartsAt     time.Time
	EndsAt       time.Time
	GeneratorURL string
	ExternalURL  string
	ReceivedAt   time.Time
	RawData      json.RawMessage
}

func Decode(decoder *json.Decoder) (Payload, error) {
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return Payload{}, fmt.Errorf("decode Alertmanager payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Payload{}, errors.New("decode Alertmanager payload: multiple JSON values")
		}
		return Payload{}, fmt.Errorf("decode Alertmanager payload: trailing data: %w", err)
	}
	if len(payload.Alerts) == 0 {
		return Payload{}, errors.New("Alertmanager payload contains no alerts")
	}
	for i := range payload.Alerts {
		if err := validateAlert(payload.Alerts[i]); err != nil {
			return Payload{}, fmt.Errorf("alert %d: %w", i, err)
		}
	}
	return payload, nil
}

func Normalize(payload Payload, sourceSlug string, receivedAt time.Time) []IncomingAlert {
	incoming := make([]IncomingAlert, 0, len(payload.Alerts))
	for _, alert := range payload.Alerts {
		fingerprint := alert.Fingerprint
		if fingerprint == "" {
			fingerprint = fingerprintLabels(alert.Labels)
		}
		rawData, _ := json.Marshal(alert)
		incoming = append(incoming, IncomingAlert{
			SourceSlug:   sourceSlug,
			Fingerprint:  fingerprint,
			Status:       alert.Status,
			Labels:       cloneMap(alert.Labels),
			Annotations:  cloneMap(alert.Annotations),
			StartsAt:     alert.StartsAt,
			EndsAt:       alert.EndsAt,
			GeneratorURL: alert.GeneratorURL,
			ExternalURL:  payload.ExternalURL,
			ReceivedAt:   receivedAt.UTC(),
			RawData:      rawData,
		})
	}
	return incoming
}

func validateAlert(alert Alert) error {
	if alert.Status != "firing" && alert.Status != "resolved" {
		return errors.New("status must be firing or resolved")
	}
	if len(alert.Labels) == 0 {
		return errors.New("labels are required")
	}
	if strings.TrimSpace(alert.Labels["alertname"]) == "" {
		return errors.New("alertname label is required")
	}
	if alert.StartsAt.IsZero() {
		return errors.New("startsAt is required")
	}
	return nil
}

func fingerprintLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	digest := sha256.New()
	for _, key := range keys {
		writeFingerprintPart(digest, key)
		writeFingerprintPart(digest, labels[key])
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeFingerprintPart(digest hash.Hash, value string) {
	_, _ = digest.Write([]byte(value))
	_, _ = digest.Write([]byte{0})
}

func cloneMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
