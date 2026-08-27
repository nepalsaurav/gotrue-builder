package gotruectl

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List every GoTrue container on this host, managed by gotruectl or not",
		Long: `Unlike "tenant list" (which only shows tenants gotruectl created), this
scans every container running a GoTrue image — so it also surfaces
instances started some other way, e.g. abc_project_app's
docker-compose.gotrue.yml stack, if it happens to be running alongside.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return statusAll()
		},
	}
}

func statusAll() error {
	containers, err := listAllContainers()
	if err != nil {
		return err
	}

	var gotrue []containerInfo
	for _, c := range containers {
		if strings.Contains(c.Image, "supabase/auth") {
			gotrue = append(gotrue, c)
		}
	}
	if len(gotrue) == 0 {
		fmt.Println("no GoTrue containers found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMANAGED\tTENANT\tPORT\tSTATE\tSTATUS\tIMAGE")
	for _, c := range gotrue {
		managed := "no"
		tenant := "-"
		if c.label("managed-by") == "gotruectl" {
			managed = "yes"
			tenant = c.label("tenant")
		}
		name := strings.TrimPrefix(c.Names, "/")
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, managed, tenant, extractHostPort(c.Ports), c.State, c.Status, c.Image)
	}
	return w.Flush()
}
