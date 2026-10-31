package main

import (
	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/store"
)

var domainCmd = &cobra.Command{
	Use:   "domain",
	Short: "Inspect hosted domains",
}

var domainListOpts directoryOpts

var domainListCmd = &cobra.Command{
	Use:   "list",
	Short: "List hosted domains (read-only)",
	Long: "Lists every hosted domain: name, enabled or disabled, the domain it is an alias\n" +
		"of (alias_of), catch-all target, user and storage quota, current usage and\n" +
		"whether a DKIM key exists. The DKIM key itself is never shown. At most 100000\n" +
		"rows are returned; a longer list is cut, a warning is printed and the exit\n" +
		"code is non-zero.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		items, trunc, err := fetchDirectory[store.DomainSummary](&domainListOpts, "/internal/domains")
		if err != nil {
			return err
		}
		return printDirectory(cmd, &domainListOpts, items, trunc,
			[]string{"DOMAIN", "STATUS", "ALIAS OF", "CATCH-ALL", "MAX USERS", "MAX STORAGE", "USAGE", "DKIM"}, domainRow)
	},
}

func init() {
	domainListOpts.addFlags(domainListCmd)
	domainCmd.AddCommand(domainListCmd)
	rootCmd.AddCommand(domainCmd)
}
