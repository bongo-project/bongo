package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bongo-go/bongo-go/internal/cluster"
)

var (
	clusterHTTPAddr string
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Cluster management commands",
}

var clusterStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster membership",
	Long:  "Query the local node's memberlist and display cluster members.",
	RunE:  runClusterStatus,
}

func init() {
	clusterStatusCmd.Flags().StringVar(&clusterHTTPAddr, "http-addr", "127.0.0.1:8080", "HTTP address of the local node")

	clusterCmd.AddCommand(clusterStatusCmd)
	rootCmd.AddCommand(clusterCmd)
}

func runClusterStatus(cmd *cobra.Command, args []string) error {
	url := fmt.Sprintf("http://%s/internal/cluster/members", clusterHTTPAddr)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", clusterHTTPAddr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	var members []cluster.MemberInfo
	if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if len(members) == 0 {
		fmt.Println("No cluster members found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Node\tAddress\tRole\tStatus")
	for _, m := range members {
		addr := fmt.Sprintf("%s:%d", m.Addr, m.Port)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Name, addr, m.Role, m.Status)
	}
	w.Flush()

	return nil
}
