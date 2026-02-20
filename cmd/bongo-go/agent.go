package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	agentMode        string
	agentConfig      string
	agentBind        string
	agentJoin        string
	bootstrapExpect  int
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Start a bongo-go agent",
	Long:  "Start a bongo-go agent in server, client, or dev mode.",
	RunE: func(cmd *cobra.Command, args []string) error {
		switch agentMode {
		case "server", "client", "dev":
		default:
			return fmt.Errorf("invalid mode %q: must be server, client, or dev", agentMode)
		}

		fmt.Printf("starting agent in %s mode\n", agentMode)
		return nil
	},
}

func init() {
	agentCmd.Flags().StringVarP(&agentMode, "mode", "m", "dev", "Agent mode: server, client, or dev")
	agentCmd.Flags().StringVarP(&agentConfig, "config", "c", "", "Path to HCL config file")
	agentCmd.Flags().StringVarP(&agentBind, "bind", "b", "0.0.0.0", "Address to bind listeners to")
	agentCmd.Flags().StringVarP(&agentJoin, "join", "j", "", "Address of an existing agent to join")
	agentCmd.Flags().IntVar(&bootstrapExpect, "bootstrap-expect", 0, "Number of expected servers to bootstrap the cluster")

	rootCmd.AddCommand(agentCmd)
}
