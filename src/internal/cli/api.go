package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/dashboard"
	"github.com/simtabi/dbpantry/src/internal/ui"
)

// apiCmd manages the dashboard API's tokens (refactor plan §8.7, X14): the
// same store the dashboard's Operations page manages.
func (a *app) apiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Manage the dashboard API: tokens for scripts",
	}
	token := &cobra.Command{
		Use:   "token",
		Short: "Create, list and revoke API tokens (Authorization: Bearer dbpantry_…)",
	}
	var scope string
	var expires time.Duration
	create := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a token, printed once: only its SHA-256 is kept",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			sc, err := dashboard.ParseScope(scope)
			if err != nil {
				return usagef("--scope: %v", err)
			}
			store, err := a.tokenStore()
			if err != nil {
				return err
			}
			plain, t, err := store.Create(args[0], sc, expires, time.Now())
			if err != nil {
				return usagef("%v", err)
			}
			if a.dryRun {
				return nil
			}
			_, err = fmt.Fprintf(a.stdout, "%s\n# %s, scope %s, id %s, expires %s; shown once\n", plain, t.Name, t.Scope, t.ID, t.Expires.Format(time.RFC3339))
			return err
		},
	}
	create.Flags().StringVar(&scope, "scope", "read", "read, operate or admin")
	create.Flags().DurationVar(&expires, "expires", 30*24*time.Hour, "how long the token lasts, e.g. 720h")
	var output string
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List the tokens (never their values)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.output(output, "table", "json"); err != nil {
				return err
			}
			store, err := a.tokenStore()
			if err != nil {
				return err
			}
			ts, err := store.List()
			if err != nil {
				return err
			}
			if output == "json" {
				if ts == nil {
					ts = []dashboard.Token{}
				}
				return writeJSON(a.stdout, ts)
			}
			t := ui.Table{Headers: []string{"NAME", "SCOPE", "ID", "EXPIRES"}}
			for _, tk := range ts {
				t.Rows = append(t.Rows, []string{tk.Name, tk.Scope.String(), tk.ID, tk.Expires.Format("2006-01-02")})
			}
			for _, l := range a.style().Table(t, a.width()) {
				if _, err := fmt.Fprintln(a.stdout, l); err != nil {
					return err
				}
			}
			return nil
		},
	}
	ls.Flags().StringVarP(&output, "output", "o", "table", "output format: table or json")
	revoke := &cobra.Command{
		Use:   "revoke NAME|ID",
		Short: "Revoke a token",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := a.tokenStore()
			if err != nil {
				return err
			}
			if err := store.Revoke(args[0]); err != nil {
				return usagef("%v", err)
			}
			_, err = fmt.Fprintf(a.stdout, "revoked %s\n", args[0])
			return err
		},
	}
	token.AddCommand(create, ls, revoke)
	cmd.AddCommand(token)
	return cmd
}

func (a *app) tokenStore() (*dashboard.TokenStore, error) {
	root, err := a.root()
	if err != nil {
		return nil, err
	}
	return dashboard.NewTokenStore(pathsFor(root, a.home(root)).tokens), nil
}
