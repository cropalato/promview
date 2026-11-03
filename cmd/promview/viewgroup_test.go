package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cropalato/promview/internal/auth"
)

type fakeViewGroupStore struct {
	group  auth.ViewGroup
	pin    auth.ViewGroupPin
	calls  []string
	listed auth.Principal
}

func (store *fakeViewGroupStore) ViewGroups(_ context.Context, principal auth.Principal) ([]auth.ViewGroupStatus, error) {
	store.listed = principal
	return []auth.ViewGroupStatus{{ViewGroup: auth.ViewGroup{Name: "platform"}}}, nil
}
func (store *fakeViewGroupStore) SetViewGroup(_ context.Context, group auth.ViewGroup) error {
	store.group = group
	store.calls = append(store.calls, "set")
	return nil
}
func (store *fakeViewGroupStore) DeleteViewGroup(_ context.Context, name string) error {
	store.calls = append(store.calls, "delete "+name)
	return nil
}
func (store *fakeViewGroupStore) PinViewGroup(_ context.Context, name string, pin auth.ViewGroupPin) error {
	store.pin = pin
	store.calls = append(store.calls, "pin "+name)
	return nil
}
func (store *fakeViewGroupStore) UnpinViewGroup(_ context.Context, name string, pin auth.ViewGroupPin) error {
	store.pin = pin
	store.calls = append(store.calls, "unpin "+name)
	return nil
}

func TestViewGroupCommand(t *testing.T) {
	store := &fakeViewGroupStore{}
	run := func(args ...string) (string, error) {
		var output bytes.Buffer
		err := runViewGroupCommand(context.Background(), store, &output, args)
		return output.String(), err
	}

	if _, err := run("set", "--name", "platform", "--description", "the team", "--selector", "team=platform", "--selector", "severity=~warn.*"); err != nil {
		t.Fatal(err)
	}
	if store.group.Name != "platform" || store.group.Description != "the team" || len(store.group.Matchers) != 2 || store.group.Matchers[1].Operator != "=~" {
		t.Fatalf("group = %#v", store.group)
	}
	if _, err := run("set", "--name", "x", "--selector", "nonsense"); err == nil {
		t.Fatal("a malformed selector must fail")
	}

	if _, err := run("pin", "--name", "platform", "--user-id", "4"); err != nil || store.pin.SubjectKind != auth.SubjectUser || store.pin.UserID != 4 {
		t.Fatalf("user pin = %#v, %v", store.pin, err)
	}
	if _, err := run("pin", "--name", "platform", "--issuer", "ldaps://ldap.example", "--group", "sre"); err != nil || store.pin.SubjectKind != auth.SubjectLDAPGroup {
		t.Fatalf("ldap pin = %#v, %v", store.pin, err)
	}
	if _, err := run("unpin", "--name", "platform", "--issuer", "https://idp.example", "--group", "sre"); err != nil || store.pin.SubjectKind != auth.SubjectOIDCGroup {
		t.Fatalf("oidc unpin = %#v, %v", store.pin, err)
	}
	if _, err := run("pin", "--name", "platform", "--user-id", "4", "--group", "sre"); err == nil {
		t.Fatal("two subjects must fail")
	}
	if _, err := run("pin", "--user-id", "4"); err == nil {
		t.Fatal("a missing name must fail")
	}

	if _, err := run("delete", "--name", "platform"); err != nil {
		t.Fatal(err)
	}
	output, err := run("list")
	if err != nil || !strings.Contains(output, `"platform"`) || !store.listed.CanAdminister() {
		t.Fatalf("list = %q, %v, as %#v", output, err, store.listed)
	}
	if _, err := run(); err == nil {
		t.Fatal("no subcommand must fail")
	}
	if _, err := run("bogus"); err == nil {
		t.Fatal("an unknown subcommand must fail")
	}
}
