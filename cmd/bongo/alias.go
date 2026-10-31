package main

import (
	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/store"
)

var aliasCmd = &cobra.Command{
	Use:   "alias",
	Short: "Inspect address aliases",
	Long: "Inspect address aliases: an alias address that delivers to an account's\n" +
		"mailbox (the /system/alias/ entries). These are not domain aliases; run\n" +
		"'bongo domain list' and read the ALIAS OF column for those.",
}

var aliasListOpts = directoryOpts{withScope: true}

var aliasListCmd = &cobra.Command{
	Use:   "list",
	Short: "List address aliases (read-only; not domain aliases)",
	Long: "Lists address aliases: alias address and the account it delivers to. These are\n" +
		"the /system/alias/ entries, not domain aliases; 'bongo domain list' shows\n" +
		"those in its ALIAS OF column. At most 100000 rows are returned; a longer list\n" +
		"is cut, a warning is printed and the exit code is non-zero.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		items, trunc, err := fetchDirectory[store.AddressAliasSummary](&aliasListOpts, "/internal/aliases")
		if err != nil {
			return err
		}
		return printDirectory(cmd, &aliasListOpts, items, trunc, []string{"ALIAS", "TARGET"}, aliasRow)
	},
}

func init() {
	aliasListOpts.addFlags(aliasListCmd)
	aliasCmd.AddCommand(aliasListCmd)
	rootCmd.AddCommand(aliasCmd)
}
