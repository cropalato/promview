package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

func TestStoreSyncSilences(t *testing.T) {
	databaseURL := os.Getenv("PROMVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PROMVIEW_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	store := New(pool)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	amURL := "http://am:9093"
	if err := store.SetSource(ctx, sources.Source{Slug: "demo", Name: "Demo", AlertmanagerURL: &amURL}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	// A preventive silence matching nothing yet, carrying the matcher shapes
	// promview never writes. It must be stored whole: no join against alerts
	// decides what enters the inventory, and neither the regex nor the negation
	// may be flattened into an equality.
	preventive := alertmanager.ListedSilence{
		ID:    "native-1",
		State: "pending",
		Matchers: []alertmanager.Matcher{
			{Name: "instance", Value: "dsm-.*", IsRegex: true, IsEqual: true},
			{Name: "team", Value: "infra", IsEqual: false},
			{Name: "severity", Value: "warning", IsEqual: true},
		},
		StartsAt:  now.Add(time.Hour),
		EndsAt:    now.Add(3 * time.Hour),
		CreatedBy: "ops@example.com",
		Comment:   "planned window",
	}
	active := alertmanager.ListedSilence{
		ID:        "native-2",
		State:     "active",
		Matchers:  []alertmanager.Matcher{{Name: "alertname", Value: "HighCPU", IsEqual: true}},
		StartsAt:  now.Add(-time.Hour),
		EndsAt:    now.Add(time.Hour),
		CreatedBy: "ada@example.com",
		Comment:   "handled",
	}
	if err := store.SyncSilences(ctx, "demo", []alertmanager.ListedSilence{preventive, active}, now); err != nil {
		t.Fatalf("SyncSilences() error = %v", err)
	}

	var state string
	var matchersJSON, listJSON []byte
	if err := pool.QueryRow(ctx,
		"SELECT state, matchers, matcher_list FROM alertmanager_silences WHERE silence_id = 'native-1'",
	).Scan(&state, &matchersJSON, &listJSON); err != nil {
		t.Fatalf("preventive silence was not stored: %v", err)
	}
	if state != "pending" {
		t.Errorf("preventive silence state = %q, want pending", state)
	}
	// The legacy map only says what it can say honestly: plain equality. The
	// regex and the negation live in the full-fidelity list.
	if want := `{"severity": "warning"}`; string(matchersJSON) != want {
		t.Errorf("equality matchers = %s, want %s", matchersJSON, want)
	}
	for _, fragment := range []string{`"isRegex":true`, `"isEqual":false`, `"dsm-.*"`} {
		if !containsCompactJSON(listJSON, fragment) {
			t.Errorf("matcher_list = %s, missing %s", listJSON, fragment)
		}
	}

	// The next listing no longer carries native-2: deleted, or garbage-collected
	// after expiring. Either way it is not suppressing, and the record must stop
	// claiming it runs until ends_at.
	if err := store.SyncSilences(ctx, "demo", []alertmanager.ListedSilence{preventive}, now.Add(time.Minute)); err != nil {
		t.Fatalf("SyncSilences() second pass error = %v", err)
	}
	var endsAt time.Time
	if err := pool.QueryRow(ctx,
		"SELECT state, ends_at FROM alertmanager_silences WHERE silence_id = 'native-2'",
	).Scan(&state, &endsAt); err != nil {
		t.Fatal(err)
	}
	if state != "expired" {
		t.Errorf("vanished silence state = %q, want expired", state)
	}
	if !endsAt.Equal(now.Add(time.Minute)) {
		t.Errorf("vanished silence ends_at = %v, want brought forward to the sync time", endsAt)
	}
	// The survivor is untouched.
	if err := pool.QueryRow(ctx,
		"SELECT state FROM alertmanager_silences WHERE silence_id = 'native-1'",
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "pending" {
		t.Errorf("listed silence state = %q, want still pending", state)
	}

	// A synced silence explains a dimmed row the way a promview-created one
	// does: with the author the Alertmanager actually holds.
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{{
		SourceSlug: "demo", Fingerprint: "cpu", Status: "firing",
		Labels:      map[string]string{"alertname": "HighCPU"},
		Annotations: map[string]string{}, StartsAt: now, ReceivedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileSource(ctx, "demo", []alertmanager.LiveAlert{
		{Fingerprint: "cpu", Suppressed: true, SilencedBy: []string{"native-2"}},
	}, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	admin := auth.Principal{UserID: 1, Subject: "ada", Grants: []auth.Grant{{Role: auth.RoleAdministrator}}}
	var alertID int64
	if err := pool.QueryRow(ctx, "SELECT id FROM alerts WHERE fingerprint = 'cpu'").Scan(&alertID); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetAlertDetail(ctx, admin, alertID)
	if err != nil {
		t.Fatalf("GetAlertDetail() error = %v", err)
	}
	if len(detail.Silences) != 1 || detail.Silences[0].CreatedBy != "ada@example.com" {
		t.Fatalf("silences = %#v, want the synced author", detail.Silences)
	}
	if detail.Silences[0].State != "expired" {
		t.Errorf("silence state = %q, want the synced expired state", detail.Silences[0].State)
	}
}

func TestStoreReconcileSourceReleasesWhenTheSilenceIsGone(t *testing.T) {
	databaseURL := os.Getenv("PROMVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PROMVIEW_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	store := New(pool)
	principal := auth.Principal{Anonymous: true}
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	amURL := "http://am:9093"
	if err := store.SetSource(ctx, sources.Source{Slug: "yul", Name: "YUL", AlertmanagerURL: &amURL}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	// The failure this closes: alerts silenced, cleared inside the window, the
	// silence then gone from the Alertmanager — which now reports no alerts at
	// all, so the empty-list guard blocks resolution and nothing left to sync
	// suppression from. `inhibited` is suppressed with no silence ids: the
	// listing says nothing about inhibitions and must leave it alone.
	for _, fingerprint := range []string{"was-silenced", "still-silenced", "inhibited"} {
		if err := store.Ingest(ctx, []alertmanager.IncomingAlert{{
			SourceSlug: "yul", Fingerprint: fingerprint, Status: "firing",
			Labels:      map[string]string{"alertname": "PrometheusTargetMissing", "instance": fingerprint},
			Annotations: map[string]string{}, StartsAt: now, ReceivedAt: now,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ReconcileSource(ctx, "yul", []alertmanager.LiveAlert{
		{Fingerprint: "was-silenced", Suppressed: true, SilencedBy: []string{"sil-gone"}},
		{Fingerprint: "still-silenced", Suppressed: true, SilencedBy: []string{"sil-live"}},
		{Fingerprint: "inhibited", Suppressed: true},
	}, nil, nil, now); err != nil {
		t.Fatal(err)
	}

	// Setting the suppression above already streamed updates; what matters below
	// is only what the release itself emits.
	beforeRelease, err := store.StreamEvents(ctx, principal, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	cursor := int64(0)
	for _, event := range beforeRelease.Events {
		if event.ID > cursor {
			cursor = event.ID
		}
	}

	// Nil makes no claim: a pass whose silence listing failed releases nothing.
	untouched, err := store.ReconcileSource(ctx, "yul", nil, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Released != 0 {
		t.Fatalf("a nil silence set released %d alerts", untouched.Released)
	}

	// An empty live view with a real silence listing: only the alert whose
	// silence vanished is released.
	result, err := store.ReconcileSource(ctx, "yul", nil, nil, map[string]bool{"sil-live": true}, now)
	if err != nil {
		t.Fatalf("ReconcileSource() error = %v", err)
	}
	if result.Released != 1 || result.Resolved != 0 || result.Suppressed != 0 {
		t.Fatalf("result = %#v, want exactly one released", result)
	}

	suppressed := map[string]bool{}
	rows, err := pool.Query(ctx, "SELECT fingerprint, suppressed FROM alerts")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var fingerprint string
		var isSuppressed bool
		if err := rows.Scan(&fingerprint, &isSuppressed); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		suppressed[fingerprint] = isSuppressed
	}
	rows.Close()
	if suppressed["was-silenced"] {
		t.Error("the alert whose silence vanished is still marked suppressed")
	}
	if !suppressed["still-silenced"] {
		t.Error("an alert with a live silence was released")
	}
	if !suppressed["inhibited"] {
		t.Error("an inhibited alert was released by the silence listing, which says nothing about inhibitions")
	}
	var silencedBy []string
	if err := pool.QueryRow(ctx,
		"SELECT silenced_by FROM alerts WHERE fingerprint = 'was-silenced'").Scan(&silencedBy); err != nil {
		t.Fatal(err)
	}
	if len(silencedBy) != 0 {
		t.Errorf("silenced_by = %v, want cleared", silencedBy)
	}

	// The console only reacts to stream events; the release must emit one.
	batch, err := store.StreamEvents(ctx, principal, cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	updated := 0
	for _, event := range batch.Events {
		if event.Type == "alert.updated" {
			updated++
		}
	}
	if updated != 1 {
		t.Errorf("alert.updated stream events = %d, want the one release", updated)
	}

	// The released alert is still firing: releasing suppression never concludes
	// an ending, that stays with the missing-set and the expiry sweep.
	var status string
	if err := pool.QueryRow(ctx,
		"SELECT source_status FROM alerts WHERE fingerprint = 'was-silenced'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != alerts.StatusFiring {
		t.Errorf("released alert status = %q, want still firing", status)
	}
}

// containsCompactJSON reports whether the compacted form of a JSON document
// carries a fragment, so an assertion does not depend on Postgres's whitespace.
func containsCompactJSON(document []byte, fragment string) bool {
	compact := make([]byte, 0, len(document))
	for _, char := range document {
		if char != ' ' && char != '\n' && char != '\t' {
			compact = append(compact, char)
		}
	}
	return strings.Contains(string(compact), fragment)
}
