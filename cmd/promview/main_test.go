package main

import (
	"context"
	"testing"

	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

type fakeSourceSetter struct {
	source  sources.Source
	token   string
	updated string
	patch   sources.Patch
	err     error
}

func (store *fakeSourceSetter) UpdateSource(_ context.Context, slug string, patch sources.Patch) error {
	store.updated = slug
	store.patch = patch
	return store.err
}

type fakeAccessStore struct {
	binding     auth.RoleBinding
	deleted     string
	diagnostics auth.AuthorizationDiagnostics
}

func (store *fakeAccessStore) SetRoleBinding(_ context.Context, binding auth.RoleBinding) error {
	store.binding = binding
	return nil
}

func (store *fakeAccessStore) DeleteRoleBinding(_ context.Context, name string) error {
	store.deleted = name
	return nil
}

func (store *fakeAccessStore) AuthorizationDiagnostics(context.Context) (auth.AuthorizationDiagnostics, error) {
	return store.diagnostics, nil
}

func (setter *fakeSourceSetter) SetSource(_ context.Context, source sources.Source, token string) error {
	setter.source = source
	setter.token = token
	return nil
}

func TestRunAccessSetCommand(t *testing.T) {
	store := &fakeAccessStore{}
	err := runAccessCommand(context.Background(), store, []string{
		"set", "--name", "platform-operators", "--role", "operator",
		"--oidc-issuer", "https://identity.example.com", "--oidc-group", "platform",
		"--selector", "team=platform", "--selector", "environment!=development",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.binding.Role != auth.RoleOperator || store.binding.SubjectKind != auth.SubjectOIDCGroup || len(store.binding.Matchers) != 2 {
		t.Fatalf("binding = %#v", store.binding)
	}
}

func TestRunAccessDeleteCommand(t *testing.T) {
	store := &fakeAccessStore{}
	if err := runAccessCommand(context.Background(), store, []string{"delete", "--name", "platform-operators"}); err != nil {
		t.Fatal(err)
	}
	if store.deleted != "platform-operators" {
		t.Fatalf("deleted = %q", store.deleted)
	}
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

func TestSourceUpdateChangesSettingsWithoutAToken(t *testing.T) {
	store := &fakeSourceSetter{}
	// The point of this command: a URL can be added without handling the
	// source's credentials, which rewriting the token would require.
	err := runSourceCommand(context.Background(), store, []string{
		"update", "--slug", "yul", "--alertmanager-url", "http://am:9093",
	})
	if err != nil {
		t.Fatalf("source update error = %v", err)
	}
	if store.updated != "yul" {
		t.Fatalf("updated slug = %q, want yul", store.updated)
	}
	if store.patch.AlertmanagerURL == nil || *store.patch.AlertmanagerURL != "http://am:9093" {
		t.Fatalf("patch = %#v, want the alertmanager URL", store.patch)
	}
	// Fields not named on the command line are left alone rather than cleared.
	if store.patch.Name != nil || store.patch.StaleAfter != nil {
		t.Errorf("patch touched unnamed fields: %#v", store.patch)
	}
	if store.token != "" {
		t.Error("source update wrote a token")
	}
}

func TestSourceUpdateRequiresASlug(t *testing.T) {
	store := &fakeSourceSetter{}
	if err := runSourceCommand(context.Background(), store, []string{"update", "--name", "New"}); err == nil {
		t.Fatal("source update without a slug error = nil, want error")
	}
}
