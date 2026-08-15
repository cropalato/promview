package alertmanager

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeAndNormalize(t *testing.T) {
	payload, err := Decode(json.NewDecoder(strings.NewReader(`{
		"version":"4",
		"externalURL":"https://alertmanager.example.com",
		"alerts":[{
			"status":"firing",
			"labels":{"team":"payments","alertname":"HighErrorRate"},
			"annotations":{"summary":"Errors are high"},
			"startsAt":"2026-08-14T12:00:00Z",
			"endsAt":"0001-01-01T00:00:00Z",
			"generatorURL":"https://prometheus.example.com/graph"
		}]
	}`)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	receivedAt := time.Date(2026, 8, 14, 12, 1, 0, 0, time.FixedZone("offset", 3600))
	alerts := Normalize(payload, "primary", receivedAt)
	if len(alerts) != 1 {
		t.Fatalf("Normalize() length = %d, want 1", len(alerts))
	}
	if alerts[0].Fingerprint == "" {
		t.Fatal("Normalize() did not derive a fingerprint")
	}
	if got := alerts[0].ReceivedAt.Location(); got != time.UTC {
		t.Fatalf("ReceivedAt location = %v, want UTC", got)
	}
	if got := alerts[0].Labels["team"]; got != "payments" {
		t.Fatalf("team label = %q, want payments", got)
	}
}

func TestDerivedFingerprintIsStable(t *testing.T) {
	first := fingerprintLabels(map[string]string{"alertname": "Down", "instance": "api-1"})
	second := fingerprintLabels(map[string]string{"instance": "api-1", "alertname": "Down"})
	if first != second {
		t.Fatalf("fingerprints differ: %q != %q", first, second)
	}
}

func TestDecodeRejectsInvalidAlert(t *testing.T) {
	_, err := Decode(json.NewDecoder(strings.NewReader(`{
		"alerts":[{"status":"unknown","labels":{"alertname":"Down"},"startsAt":"2026-08-14T12:00:00Z"}]
	}`)))
	if err == nil {
		t.Fatal("Decode() error = nil, want validation error")
	}
}
