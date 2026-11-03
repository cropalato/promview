package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/cropalato/promview/internal/auth"
)

const viewGroupUsage = "usage: promview view-group [set|delete|list|pin|unpin]"

type viewGroupStore interface {
	ViewGroups(context.Context, auth.Principal) ([]auth.ViewGroupStatus, error)
	SetViewGroup(context.Context, auth.ViewGroup) error
	DeleteViewGroup(context.Context, string) error
	PinViewGroup(context.Context, string, auth.ViewGroupPin) error
	UnpinViewGroup(context.Context, string, auth.ViewGroupPin) error
}

// runViewGroupCommand administers view groups from the server's shell, the same
// way `promview access` administers bindings. A view group narrows what users
// see by default and never grants access, so nothing here can widen scope.
func runViewGroupCommand(ctx context.Context, store viewGroupStore, output io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New(viewGroupUsage)
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: promview view-group list")
		}
		// The shell is the administrator, so the listing includes selectors,
		// pins, and member counts.
		groups, err := store.ViewGroups(ctx, auth.Principal{
			Roles:  []string{string(auth.RoleAdministrator)},
			Grants: []auth.Grant{{Role: auth.RoleAdministrator}},
		})
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]any{"groups": groups})
	case "delete":
		flags := flag.NewFlagSet("promview view-group delete", flag.ContinueOnError)
		name := flags.String("name", "", "view group name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("--name is required")
		}
		return store.DeleteViewGroup(ctx, *name)
	case "set":
		flags := flag.NewFlagSet("promview view-group set", flag.ContinueOnError)
		name := flags.String("name", "", "view group name")
		description := flags.String("description", "", "what the group is for")
		var selectors repeatedStrings
		flags.Var(&selectors, "selector", "label selector; repeat for AND semantics")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		group := auth.ViewGroup{Name: *name, Description: *description}
		for _, raw := range selectors {
			matcher, err := auth.ParseLabelMatcher(raw)
			if err != nil {
				return err
			}
			group.Matchers = append(group.Matchers, matcher)
		}
		return store.SetViewGroup(ctx, group)
	case "pin", "unpin":
		flags := flag.NewFlagSet("promview view-group "+args[0], flag.ContinueOnError)
		name := flags.String("name", "", "view group name")
		userID := flags.Int64("user-id", 0, "Promview user ID")
		issuer := flags.String("issuer", "", "directory issuer URL")
		group := flags.String("group", "", "group name within the issuer")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("--name is required")
		}
		pin := auth.ViewGroupPin{}
		switch {
		case *userID > 0 && *issuer == "" && *group == "":
			pin.SubjectKind = auth.SubjectUser
			pin.UserID = *userID
		case *userID == 0 && *issuer != "" && *group != "":
			// The issuer scheme names the directory, as it does for bindings.
			pin.SubjectKind = auth.SubjectOIDCGroup
			if scheme, _, found := strings.Cut(*issuer, "://"); found && (scheme == "ldap" || scheme == "ldaps") {
				pin.SubjectKind = auth.SubjectLDAPGroup
			}
			pin.SubjectIssuer = *issuer
			pin.SubjectGroup = *group
		default:
			return errors.New("set exactly one subject with --user-id or --issuer and --group")
		}
		if args[0] == "pin" {
			return store.PinViewGroup(ctx, *name, pin)
		}
		return store.UnpinViewGroup(ctx, *name, pin)
	default:
		return errors.New(viewGroupUsage)
	}
}
