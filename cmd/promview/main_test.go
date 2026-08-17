package main

import (
	"context"
	"testing"

	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

type fakeSourceSetter struct {
	source sources.Source
	token  string
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
