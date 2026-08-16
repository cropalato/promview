package main

import (
	"context"
	"testing"

	"github.com/cropalato/promview/internal/sources"
)

type fakeSourceSetter struct {
	source sources.Source
	token  string
}

func (setter *fakeSourceSetter) SetSource(_ context.Context, source sources.Source, token string) error {
	setter.source = source
	setter.token = token
	return nil
}

func TestRunSourceCommand(t *testing.T) {
	store := &fakeSourceSetter{}
	err := runSourceCommand(context.Background(), store, []string{
		"set", "--slug", "primary", "--name", "Primary", "--token", "0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.source.Slug != "primary" || store.source.Name != "Primary" || store.token != "0123456789abcdef" {
		t.Fatalf("source = %#v, token = %q", store.source, store.token)
	}
}
