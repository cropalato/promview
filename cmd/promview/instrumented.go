package main

import (
	"context"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/metrics"
	"github.com/cropalato/promview/internal/postgres"
)

/*
Counting the two silence outcomes that are otherwise invisible.

Both wrap rather than reach into the packages that do the work. The transport
should not have to know a registry exists, and the store even less so; wiring is
this file's job, and a decorator is the seam that already existed for it.
*/

// countingSilencer records whether a silence reached its Alertmanager.
type countingSilencer struct {
	inner   httpSilencer
	metrics *metrics.Metrics
}

func (silencer countingSilencer) CreateSilence(
	ctx context.Context,
	baseURL string,
	token string,
	silence alertmanager.Silence,
) (string, error) {
	id, err := silencer.inner.CreateSilence(ctx, baseURL, token, silence)
	// Labelled by Alertmanager, not by source: this seam knows the URL it wrote
	// to and one Alertmanager can serve several sources. Both are bounded by
	// configuration, so neither can grow a time series per alert.
	silencer.metrics.SilenceWritten(baseURL, err)
	return id, err
}

// countingStore records whether a created silence's provenance was stored.
//
// Embedded rather than reimplemented: the store interface is wide, and this
// cares about exactly one method. Everything else passes straight through, and
// a method added later needs no change here.
type countingStore struct {
	*postgres.Store
	metrics *metrics.Metrics
}

func (store countingStore) RecordSilence(ctx context.Context, record alerts.SilenceRecord) error {
	err := store.Store.RecordSilence(ctx, record)
	// A failure here deliberately does not fail the silence - it already exists
	// at the Alertmanager - so this counter is the only thing that ever says it
	// happened. Losing the record quietly is how a silence becomes
	// unexplainable later.
	store.metrics.SilenceRecorded(err)
	return err
}
