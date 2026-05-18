package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"kube-insight/internal/storage/clickhouse"

	"github.com/spf13/cobra"
)

type clickHouseServiceFactsBackfillOutput struct {
	Endpoint string                                `json:"endpoint"`
	Database string                                `json:"database"`
	Report   clickhouse.ServiceFactsBackfillReport `json:"report"`
}

func dbClickHouseBackfillServiceFactsCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	var endpoint string
	var database string
	var clusterID string
	var namespace string
	var batchSize int
	var yes bool
	cmd := &cobra.Command{
		Use:   "backfill-service-facts",
		Short: "Backfill missing ClickHouse service.* facts from retained Service versions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if endpoint == "" {
				endpoint = os.Getenv(rt.Config.Storage.ClickHouse.DSNEnv)
			}
			if endpoint == "" {
				return fmt.Errorf("clickhouse backfill-service-facts requires --endpoint or env %s", rt.Config.Storage.ClickHouse.DSNEnv)
			}
			if database == "" {
				database = rt.Config.Storage.ClickHouse.Database
			}
			if yes {
				if err := requireWriteRole(rt.Config, "db clickhouse backfill-service-facts"); err != nil {
					return err
				}
			}
			client := clickhouse.HTTPClient{Endpoint: endpoint, AsyncInsert: rt.Config.Storage.ClickHouse.AsyncInsert}
			report, err := clickhouse.BackfillServiceFacts(ctx, client, client, clickhouse.ServiceFactsBackfillOptions{
				Database:  database,
				ClusterID: clusterID,
				Namespace: namespace,
				BatchSize: batchSize,
				DryRun:    !yes,
			})
			if err != nil {
				return err
			}
			out := clickHouseServiceFactsBackfillOutput{
				Endpoint: redactClickHouseEndpoint(endpoint),
				Database: report.Database,
				Report:   report,
			}
			if output == outputJSON {
				return writeJSON(stdout, out)
			}
			return writeClickHouseServiceFactsBackfillTable(stdout, out)
		},
	}
	addOutputFlag(cmd, &output, outputTable)
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "ClickHouse HTTP endpoint; defaults to env storage.clickhouse.dsnEnv")
	cmd.Flags().StringVar(&database, "database", "", "ClickHouse database name; defaults to storage.clickhouse.database")
	cmd.Flags().StringVar(&clusterID, "cluster", "", "Optional kube-insight cluster ID/name filter")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Optional namespace filter")
	cmd.Flags().IntVar(&batchSize, "batch-size", 500, "Service versions per backfill batch")
	cmd.Flags().BoolVar(&yes, "yes", false, "Apply backfill; without --yes this is a dry run")
	return cmd
}

func dbClickHouseRepairEdgeKindsCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	var endpoint string
	var database string
	var clusterID string
	var yes bool
	cmd := &cobra.Command{
		Use:   "repair-edge-kinds",
		Short: "Repair missing ClickHouse edge src_kind/dst_kind values from logical IDs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if endpoint == "" {
				endpoint = os.Getenv(rt.Config.Storage.ClickHouse.DSNEnv)
			}
			if endpoint == "" {
				return fmt.Errorf("clickhouse repair-edge-kinds requires --endpoint or env %s", rt.Config.Storage.ClickHouse.DSNEnv)
			}
			if database == "" {
				database = rt.Config.Storage.ClickHouse.Database
			}
			if yes {
				if err := requireWriteRole(rt.Config, "db clickhouse repair-edge-kinds"); err != nil {
					return err
				}
			}
			client := clickhouse.HTTPClient{Endpoint: endpoint}
			report, err := clickhouse.RepairEdgeKinds(ctx, client, client, clickhouse.EdgeKindRepairOptions{
				Database:  database,
				ClusterID: clusterID,
				DryRun:    !yes,
			})
			if err != nil {
				return err
			}
			out := clickHouseEdgeKindRepairOutput{
				Endpoint: redactClickHouseEndpoint(endpoint),
				Database: report.Database,
				Report:   report,
			}
			if output == outputJSON {
				return writeJSON(stdout, out)
			}
			return writeClickHouseEdgeKindRepairTable(stdout, out)
		},
	}
	addOutputFlag(cmd, &output, outputTable)
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "ClickHouse HTTP endpoint; defaults to env storage.clickhouse.dsnEnv")
	cmd.Flags().StringVar(&database, "database", "", "ClickHouse database name; defaults to storage.clickhouse.database")
	cmd.Flags().StringVar(&clusterID, "cluster", "", "Optional kube-insight cluster ID/name filter")
	cmd.Flags().BoolVar(&yes, "yes", false, "Apply edge kind repair mutations; without --yes this is a dry run")
	return cmd
}

func writeClickHouseServiceFactsBackfillTable(stdout io.Writer, out clickHouseServiceFactsBackfillOutput) error {
	mode := "dry-run"
	insertLabel := "to insert"
	if !out.Report.DryRun {
		mode = "apply"
		insertLabel = "inserted"
	}
	return writeTable(stdout, []string{"metric", "value"}, [][]string{
		{"endpoint", out.Endpoint},
		{"database", out.Database},
		{"mode", mode},
		{"batches", humanCount(out.Report.Batches)},
		{"service versions scanned", humanCount(out.Report.VersionsScanned)},
		{"facts " + insertLabel, humanCount(out.Report.FactsInserted)},
		{"changes " + insertLabel, humanCount(out.Report.ChangesInserted)},
		{"duration", humanDuration(out.Report.StartedAt, out.Report.FinishedAt)},
	})
}

func writeClickHouseEdgeKindRepairTable(stdout io.Writer, out clickHouseEdgeKindRepairOutput) error {
	mode := "dry-run"
	if !out.Report.DryRun {
		mode = "apply"
	}
	return writeTable(stdout, []string{"metric", "value"}, [][]string{
		{"endpoint", out.Endpoint},
		{"database", out.Database},
		{"mode", mode},
		{"src kind missing", humanCount(out.Report.SrcMissing)},
		{"dst kind missing", humanCount(out.Report.DstMissing)},
		{"src kind repairable", humanCount(out.Report.SrcRepairable)},
		{"dst kind repairable", humanCount(out.Report.DstRepairable)},
		{"statements", humanCount(int64(len(out.Report.Statements)))},
		{"applied", humanCount(int64(out.Report.Applied))},
		{"duration", humanDuration(out.Report.StartedAt, out.Report.FinishedAt)},
	})
}
