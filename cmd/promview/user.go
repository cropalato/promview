package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cropalato/promview/internal/postgres"
)

type userStore interface {
	CreateLocalAccount(context.Context, postgres.LocalAccount, string) (int64, error)
	SetLocalPassword(context.Context, string, string) error
	UnlockLocalAccount(context.Context, string) error
	SetLocalAccountEnabled(context.Context, string, bool) error
	ListLocalAccounts(context.Context) ([]postgres.LocalAccount, error)
}

func runUserCommand(ctx context.Context, store userStore, stdin io.Reader, stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: promview user [create|set-password|unlock|enable|disable|list]")
	}
	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("promview user create", flag.ContinueOnError)
		username := flags.String("username", "", "login name")
		email := flags.String("email", "", "email address")
		displayName := flags.String("display-name", "", "name shown in the console")
		fromStdin := flags.Bool("password-stdin", false, "read the password from standard input")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		password, err := readPassword(stdin, *fromStdin)
		if err != nil {
			return err
		}
		userID, err := store.CreateLocalAccount(ctx, postgres.LocalAccount{
			Username: *username, Email: *email, DisplayName: *displayName,
		}, password)
		if err != nil {
			return err
		}
		// The ID is what a role binding names, and an administrator has to
		// bind the account to something before it can read anything at all.
		fmt.Fprintf(stdout, "created user %d\n", userID)
		fmt.Fprintf(stdout, "grant it access with: promview access set --name <binding> --role viewer --user-id %d\n", userID)
		return nil

	case "set-password":
		flags := flag.NewFlagSet("promview user set-password", flag.ContinueOnError)
		username := flags.String("username", "", "login name")
		fromStdin := flags.Bool("password-stdin", false, "read the password from standard input")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		password, err := readPassword(stdin, *fromStdin)
		if err != nil {
			return err
		}
		return store.SetLocalPassword(ctx, *username, password)

	case "unlock":
		flags := flag.NewFlagSet("promview user unlock", flag.ContinueOnError)
		username := flags.String("username", "", "login name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return store.UnlockLocalAccount(ctx, *username)

	case "enable", "disable":
		flags := flag.NewFlagSet("promview user "+args[0], flag.ContinueOnError)
		username := flags.String("username", "", "login name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return store.SetLocalAccountEnabled(ctx, *username, args[0] == "enable")

	case "list":
		accounts, err := store.ListLocalAccounts(ctx)
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tUSERNAME\tSTATE\tLAST LOGIN")
		for _, account := range accounts {
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\n",
				account.UserID, account.Username, accountState(account), lastLogin(account))
		}
		return writer.Flush()

	default:
		return fmt.Errorf("unknown user command %q", args[0])
	}
}

// accountState folds enabled and locked into one column, because those are the
// two reasons somebody cannot sign in and an administrator is usually running
// this command to find out which.
func accountState(account postgres.LocalAccount) string {
	switch {
	case !account.Enabled:
		return "disabled"
	case !account.LockedUntil.IsZero() && account.LockedUntil.After(time.Now().UTC()):
		return "locked until " + account.LockedUntil.Format(time.RFC3339)
	default:
		return "enabled"
	}
}

func lastLogin(account postgres.LocalAccount) string {
	if account.LastLoginAt.IsZero() {
		return "never"
	}
	return account.LastLoginAt.Format(time.RFC3339)
}

// readPassword takes the password from standard input or from the environment,
// never from a flag.
//
// A --password flag would put the password in argv, which is world-readable
// through /proc and lands in shell history. --password-stdin is what docker
// login does and is the reason it is safe to paste into a terminal.
func readPassword(stdin io.Reader, fromStdin bool) (string, error) {
	if fromStdin {
		password, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		// Only the trailing newline: a password may legitimately begin or end
		// with a space, and trimming it would set something other than what was
		// typed while reporting success.
		return strings.TrimRight(password, "\r\n"), nil
	}
	if password, set := os.LookupEnv("PROMVIEW_LOCAL_PASSWORD"); set {
		return password, nil
	}
	return "", errors.New("pass --password-stdin or set PROMVIEW_LOCAL_PASSWORD")
}
