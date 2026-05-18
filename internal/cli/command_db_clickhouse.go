package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"kube-insight/internal/storage"
	"kube-insight/internal/storage/clickhouse"

	"github.com/spf13/cobra"
)

const outputSQL = "sql"

type clickHouseSchemaFlags struct {
	Database    string
	Cluster     string
	HotVolume   string
	ColdVolume  string
	ColdAfter   time.Duration
	UseJSONType bool
}

type clickHouseSchemaOutput struct {
	Database   string   `json:"database"`
	Cluster    string   `json:"cluster,omitempty"`
	UseJSON    bool     `json:"useJsonType"`
	Statements []string `json:"statements"`
}

type clickHouseInitOutput struct {
	Endpoint   string `json:"endpoint"`
	Database   string `json:"database"`
	Statements int    `json:"statements"`
	Applied    int    `json:"applied"`
}

type clickHouseImportOutput struct {
	Endpoint string         `json:"endpoint"`
	Database string         `json:"database"`
	Summary  any            `json:"summary"`
	Tables   map[string]int `json:"tables"`
	Rows     int            `json:"rows"`
}

type clickHouseStatusOutput struct {
	Endpoint string                         `json:"endpoint"`
	Database string                         `json:"database"`
	OK       bool                           `json:"ok"`
	Tables   []clickhouse.TableSchemaStatus `json:"tables"`
}

type clickHouseRepairOutput struct {
	Endpoint string                      `json:"endpoint"`
	Database string                      `json:"database"`
	DryRun   bool                        `json:"dryRun"`
	Applied  int                         `json:"applied,omitempty"`
	Plan     clickhouse.SchemaRepairPlan `json:"plan"`
}

type clickHouseCleanupOutput struct {
	Endpoint string                               `json:"endpoint"`
	Database string                               `json:"database"`
	DryRun   bool                                 `json:"dryRun"`
	Applied  int                                  `json:"applied,omitempty"`
	Plan     clickhouse.RepairArtifactCleanupPlan `json:"plan"`
}

func dbClickHouseCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clickhouse",
		Short: "Manage ClickHouse evidence backend artifacts.",
	}
	cmd.AddCommand(dbClickHouseSchemaCommand(ctx, stdout, state))
	cmd.AddCommand(dbClickHouseInitCommand(ctx, stdout, state))
	cmd.AddCommand(dbClickHouseStatusCommand(ctx, stdout, state))
	cmd.AddCommand(dbClickHouseRepairCommand(ctx, stdout, state))
	cmd.AddCommand(dbClickHouseCleanupCommand(ctx, stdout, state))
	cmd.AddCommand(dbClickHouseImportCommand(ctx, stdout, state))
	cmd.AddCommand(dbClickHouseServiceCommand(ctx, stdout, state))
	cmd.AddCommand(dbClickHouseBackfillServiceFactsCommand(ctx, stdout, state))
	return cmd
}

func dbClickHouseSchemaCommand(_ context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	var flags clickHouseSchemaFlags
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print ClickHouse schema DDL for the evidence backend.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateClickHouseSchemaOutput(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			schemaOpts := clickHouseSchemaOptionsFromCommand(cmd, rt, flags)
			statements, err := clickhouse.CreateTableStatements(schemaOpts)
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, clickHouseSchemaOutput{
					Database:   schemaOpts.Database,
					Cluster:    schemaOpts.Cluster,
					UseJSON:    schemaOpts.UseJSONType,
					Statements: statements,
				})
			}
			return writeSQLStatements(stdout, statements)
		},
	}
	cmd.Flags().StringVar(&output, "output", outputSQL, "Output format: sql or json")
	addClickHouseSchemaFlags(cmd, &flags)
	return cmd
}

func dbClickHouseInitCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	var endpoint string
	var flags clickHouseSchemaFlags
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Apply ClickHouse schema DDL over the HTTP interface.",
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
				return fmt.Errorf("clickhouse init requires --endpoint or env %s", rt.Config.Storage.ClickHouse.DSNEnv)
			}
			schemaOpts := clickHouseSchemaOptionsFromCommand(cmd, rt, flags)
			statements, err := clickhouse.CreateTableStatements(schemaOpts)
			if err != nil {
				return err
			}
			result, err := clickhouse.HTTPClient{Endpoint: endpoint}.ApplySchema(ctx, statements)
			if err != nil {
				return err
			}
			out := clickHouseInitOutput{
				Endpoint:   redactClickHouseEndpoint(result.Endpoint),
				Database:   schemaOpts.Database,
				Statements: result.Statements,
				Applied:    result.Applied,
			}
			if output == outputJSON {
				return writeJSON(stdout, out)
			}
			return writeClickHouseInitTable(stdout, out)
		},
	}
	addOutputFlag(cmd, &output, outputTable)
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "ClickHouse HTTP endpoint; defaults to env storage.clickhouse.dsnEnv")
	addClickHouseSchemaFlags(cmd, &flags)
	return cmd
}

func dbClickHouseStatusCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	var endpoint string
	var flags clickHouseSchemaFlags
	var strict bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check existing ClickHouse table engines and sorting keys.",
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
				return fmt.Errorf("clickhouse status requires --endpoint or env %s", rt.Config.Storage.ClickHouse.DSNEnv)
			}
			schemaOpts := clickHouseSchemaOptionsFromCommand(cmd, rt, flags)
			status, err := clickhouse.CheckSchemaStatus(ctx, clickhouse.HTTPClient{Endpoint: endpoint}, schemaOpts)
			if err != nil {
				return err
			}
			out := clickHouseStatusOutput{
				Endpoint: redactClickHouseEndpoint(endpoint),
				Database: status.Database,
				OK:       status.OK,
				Tables:   status.Tables,
			}
			if output == outputJSON {
				if err := writeJSON(stdout, out); err != nil {
					return err
				}
			} else if err := writeClickHouseStatusTable(stdout, out); err != nil {
				return err
			}
			if strict && !status.OK {
				return fmt.Errorf("clickhouse schema status has drift")
			}
			return nil
		},
	}
	addOutputFlag(cmd, &output, outputTable)
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "ClickHouse HTTP endpoint; defaults to env storage.clickhouse.dsnEnv")
	cmd.Flags().BoolVar(&strict, "strict", false, "Exit non-zero when schema drift is detected")
	addClickHouseSchemaFlags(cmd, &flags)
	return cmd
}

func dbClickHouseRepairCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	var endpoint string
	var flags clickHouseSchemaFlags
	var apply bool
	var yes bool
	var suffix string
	cmd := &cobra.Command{
		Use:   "repair-ingestion-offsets",
		Short: "Plan or apply the safe ingestion_offsets schema repair.",
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
				return fmt.Errorf("clickhouse repair-ingestion-offsets requires --endpoint or env %s", rt.Config.Storage.ClickHouse.DSNEnv)
			}
			schemaOpts := clickHouseSchemaOptionsFromCommand(cmd, rt, flags)
			client := clickhouse.HTTPClient{Endpoint: endpoint}
			status, err := clickhouse.CheckSchemaStatus(ctx, client, schemaOpts)
			if err != nil {
				return err
			}
			if suffix == "" {
				suffix = time.Now().UTC().Format("20060102_150405")
			}
			plan, err := clickhouse.PlanIngestionOffsetsRepair(status, schemaOpts, suffix)
			if err != nil {
				return err
			}
			out := clickHouseRepairOutput{
				Endpoint: redactClickHouseEndpoint(endpoint),
				Database: schemaOpts.Database,
				DryRun:   !apply,
				Plan:     plan,
			}
			if apply && plan.Needed {
				if err := requireWriteRole(rt.Config, "db clickhouse repair-ingestion-offsets"); err != nil {
					return err
				}
				if !yes {
					return fmt.Errorf("clickhouse repair-ingestion-offsets requires --yes with --apply")
				}
				result, err := client.ApplySchema(ctx, plan.Statements)
				out.Applied = result.Applied
				if err != nil {
					return err
				}
			}
			if output == outputJSON {
				return writeJSON(stdout, out)
			}
			return writeClickHouseRepairTable(stdout, out)
		},
	}
	addOutputFlag(cmd, &output, outputTable)
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "ClickHouse HTTP endpoint; defaults to env storage.clickhouse.dsnEnv")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply the generated repair statements")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm applying the repair statements")
	cmd.Flags().StringVar(&suffix, "suffix", "", "Suffix for scratch and backup tables; defaults to a UTC timestamp")
	addClickHouseSchemaFlags(cmd, &flags)
	return cmd
}

func dbClickHouseCleanupCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	var endpoint string
	var flags clickHouseSchemaFlags
	var yes bool
	cmd := &cobra.Command{
		Use:   "cleanup-repair-artifacts",
		Short: "List or clean safe ClickHouse repair scratch artifacts.",
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
				return fmt.Errorf("clickhouse cleanup-repair-artifacts requires --endpoint or env %s", rt.Config.Storage.ClickHouse.DSNEnv)
			}
			schemaOpts := clickHouseSchemaOptionsFromCommand(cmd, rt, flags)
			client := clickhouse.HTTPClient{Endpoint: endpoint}
			artifacts, err := clickhouse.ListRepairArtifacts(ctx, client, schemaOpts)
			if err != nil {
				return err
			}
			plan, err := clickhouse.PlanRepairArtifactCleanup(artifacts, schemaOpts)
			if err != nil {
				return err
			}
			out := clickHouseCleanupOutput{
				Endpoint: redactClickHouseEndpoint(endpoint),
				Database: schemaOpts.Database,
				DryRun:   !yes,
				Plan:     plan,
			}
			if yes && len(plan.Statements) > 0 {
				if err := requireWriteRole(rt.Config, "db clickhouse cleanup-repair-artifacts"); err != nil {
					return err
				}
				result, err := client.ApplySchema(ctx, plan.Statements)
				out.Applied = result.Applied
				if err != nil {
					return err
				}
			}
			if output == outputJSON {
				return writeJSON(stdout, out)
			}
			return writeClickHouseCleanupTable(stdout, out)
		},
	}
	addOutputFlag(cmd, &output, outputTable)
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "ClickHouse HTTP endpoint; defaults to env storage.clickhouse.dsnEnv")
	cmd.Flags().BoolVar(&yes, "yes", false, "Drop empty repair scratch tables; backup tables are never dropped")
	addClickHouseSchemaFlags(cmd, &flags)
	return cmd
}

func dbClickHouseServiceCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var endpoint string
	var database string
	var clusterID string
	var limit int
	var maxVersionsPerObject int
	cmd := &cobra.Command{
		Use:   "service NAMESPACE NAME",
		Short: "Query ClickHouse service investigation for a namespace/name.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if endpoint == "" {
				endpoint = os.Getenv(rt.Config.Storage.ClickHouse.DSNEnv)
			}
			if endpoint == "" {
				return fmt.Errorf("clickhouse service requires --endpoint or env %s", rt.Config.Storage.ClickHouse.DSNEnv)
			}
			if database == "" {
				database = rt.Config.Storage.ClickHouse.Database
			}
			opts := clickhouse.OptionsFromConfig(rt.Config.Storage.ClickHouse)
			opts.Database = database
			store, err := clickhouse.NewHTTPStore(endpoint, opts)
			if err != nil {
				return err
			}
			defer store.Close()
			result, err := store.InvestigateServiceWithOptions(ctx, storage.ObjectTarget{
				ClusterID: clusterID,
				Kind:      "Service",
				Namespace: args[0],
				Name:      args[1],
			}, storage.InvestigationOptions{
				MaxEvidenceObjects:   limit,
				MaxVersionsPerObject: maxVersionsPerObject,
			})
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "ClickHouse HTTP endpoint; defaults to env storage.clickhouse.dsnEnv")
	cmd.Flags().StringVar(&database, "database", "", "ClickHouse database name; defaults to storage.clickhouse.database")
	cmd.Flags().StringVar(&clusterID, "cluster", "", "Optional kube-insight cluster ID/name filter")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum related evidence objects")
	cmd.Flags().IntVar(&maxVersionsPerObject, "max-versions-per-object", 3, "Maximum retained versions per evidence object")
	return cmd
}

func dbClickHouseImportCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	var endpoint string
	var file string
	var dir string
	var flags clickHouseSchemaFlags
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Ingest Kubernetes JSON and import derived evidence into ClickHouse.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			if (file == "") == (dir == "") {
				return fmt.Errorf("clickhouse import requires exactly one of --file or --dir")
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if err := requireWriteRole(rt.Config, "db clickhouse import"); err != nil {
				return err
			}
			if endpoint == "" {
				endpoint = os.Getenv(rt.Config.Storage.ClickHouse.DSNEnv)
			}
			if endpoint == "" {
				return fmt.Errorf("clickhouse import requires --endpoint or env %s", rt.Config.Storage.ClickHouse.DSNEnv)
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			memory := storage.NewMemoryStore()
			summary, err := ingestInputs(runCtx, memory, file, dir, rt.Config)
			if err != nil {
				return err
			}
			schemaOpts := clickHouseSchemaOptionsFromCommand(cmd, rt, flags)
			batch, err := clickhouse.BuildEvidenceBatch(schemaOpts.Database, memory.Observations, memory.Facts, memory.Edges, memory.Changes)
			if err != nil {
				return err
			}
			result, err := clickhouse.HTTPClient{Endpoint: endpoint, AsyncInsert: rt.Config.Storage.ClickHouse.AsyncInsert}.InsertEvidenceBatch(runCtx, batch)
			if err != nil {
				return err
			}
			out := clickHouseImportOutput{
				Endpoint: redactClickHouseEndpoint(result.Endpoint),
				Database: result.Database,
				Summary:  summary,
				Tables:   result.Tables,
				Rows:     result.Rows,
			}
			if output == outputJSON {
				return writeJSON(stdout, out)
			}
			return writeClickHouseImportTable(stdout, out)
		},
	}
	addOutputFlag(cmd, &output, outputTable)
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "ClickHouse HTTP endpoint; defaults to env storage.clickhouse.dsnEnv")
	cmd.Flags().StringVarP(&file, "file", "f", "", "Kubernetes JSON file to ingest and import")
	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Directory of Kubernetes JSON files to ingest and import")
	addClickHouseSchemaFlags(cmd, &flags)
	return cmd
}

func addClickHouseSchemaFlags(cmd *cobra.Command, flags *clickHouseSchemaFlags) {
	cmd.Flags().StringVar(&flags.Database, "database", "", "ClickHouse database name; defaults to storage.clickhouse.database")
	cmd.Flags().StringVar(&flags.Cluster, "cluster", "", "Optional ClickHouse cluster name for ON CLUSTER DDL")
	cmd.Flags().StringVar(&flags.HotVolume, "hot-volume", "", "Hot storage volume name for documentation and future policies")
	cmd.Flags().StringVar(&flags.ColdVolume, "cold-volume", "", "Cold storage volume name for TTL TO VOLUME")
	cmd.Flags().DurationVar(&flags.ColdAfter, "cold-after", 0, "Move parts to cold volume after this duration; 0 disables TTL")
	cmd.Flags().BoolVar(&flags.UseJSONType, "json-type", false, "Use ClickHouse JSON type for retained documents instead of String CODEC(ZSTD)")
}

func clickHouseSchemaOptionsFromCommand(cmd *cobra.Command, rt runtimeSettings, flags clickHouseSchemaFlags) clickhouse.SchemaOptions {
	cfg := rt.Config.Storage.ClickHouse
	if !cmd.Flags().Changed("database") {
		flags.Database = cfg.Database
	}
	if !cmd.Flags().Changed("cluster") {
		flags.Cluster = cfg.Cluster
	}
	if !cmd.Flags().Changed("hot-volume") {
		flags.HotVolume = cfg.HotVolume
	}
	if !cmd.Flags().Changed("cold-volume") {
		flags.ColdVolume = cfg.ColdVolume
	}
	if !cmd.Flags().Changed("cold-after") && cfg.ColdAfterSeconds > 0 {
		flags.ColdAfter = time.Duration(cfg.ColdAfterSeconds) * time.Second
	}
	if !cmd.Flags().Changed("json-type") {
		flags.UseJSONType = cfg.UseJSONType
	}
	return clickhouse.SchemaOptions{
		Database:         flags.Database,
		Cluster:          flags.Cluster,
		HotVolume:        flags.HotVolume,
		ColdVolume:       flags.ColdVolume,
		ColdAfterSeconds: int(flags.ColdAfter / time.Second),
		UseJSONType:      flags.UseJSONType,
	}
}

func validateClickHouseSchemaOutput(format string) error {
	switch format {
	case outputSQL, outputJSON:
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; use sql or json", format)
	}
}

func writeSQLStatements(stdout io.Writer, statements []string) error {
	for i, statement := range statements {
		if i > 0 {
			if _, err := fmt.Fprintln(stdout); err != nil {
				return err
			}
		}
		text := strings.TrimSpace(statement)
		if _, err := fmt.Fprintln(stdout, text+";"); err != nil {
			return err
		}
	}
	return nil
}

func redactClickHouseEndpoint(endpoint string) string {
	before, after, ok := strings.Cut(endpoint, "?")
	if !ok {
		return redactPasswordQueryValue(endpoint)
	}
	return before + "?" + redactPasswordQueryValue(after)
}

func redactPasswordQueryValue(value string) string {
	parts := strings.Split(value, "&")
	for i, part := range parts {
		if strings.HasPrefix(part, "password=") {
			parts[i] = "password=***"
		}
	}
	return strings.Join(parts, "&")
}

func writeClickHouseStatusTable(stdout io.Writer, out clickHouseStatusOutput) error {
	if err := writeTable(stdout, []string{"setting", "value"}, [][]string{
		{"endpoint", out.Endpoint},
		{"database", out.Database},
		{"ok", boolText(out.OK)},
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	rows := make([][]string, 0, len(out.Tables))
	for _, table := range out.Tables {
		problem := ""
		if len(table.Problems) > 0 {
			problem = strings.Join(table.Problems, "; ")
		}
		rows = append(rows, []string{
			table.Name,
			boolText(table.OK),
			table.Engine,
			table.ExpectedEngine,
			table.SortingKey,
			table.ExpectedSortingKey,
			problem,
		})
	}
	return writeTable(stdout, []string{"table", "ok", "engine", "expected engine", "sorting key", "expected sorting key", "problem"}, rows)
}

func writeClickHouseCleanupTable(stdout io.Writer, out clickHouseCleanupOutput) error {
	droppable := 0
	for _, artifact := range out.Plan.Artifacts {
		if artifact.DropEligible {
			droppable++
		}
	}
	if err := writeTable(stdout, []string{"setting", "value"}, [][]string{
		{"endpoint", out.Endpoint},
		{"database", out.Database},
		{"dryRun", boolText(out.DryRun)},
		{"artifacts", fmt.Sprintf("%d", len(out.Plan.Artifacts))},
		{"droppable", fmt.Sprintf("%d", droppable)},
		{"applied", fmt.Sprintf("%d", out.Applied)},
	}); err != nil {
		return err
	}
	if len(out.Plan.Artifacts) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	rows := make([][]string, 0, len(out.Plan.Artifacts))
	for _, artifact := range out.Plan.Artifacts {
		action := "keep"
		if artifact.DropEligible {
			action = "drop-empty-repair"
		}
		rows = append(rows, []string{
			artifact.Name,
			artifact.Kind,
			artifact.Engine,
			fmt.Sprintf("%d", artifact.Rows),
			fmt.Sprintf("%d", artifact.Bytes),
			boolText(artifact.DropEligible),
			action,
		})
	}
	return writeTable(stdout, []string{"table", "kind", "engine", "rows", "bytes", "drop eligible", "action"}, rows)
}

func writeClickHouseRepairTable(stdout io.Writer, out clickHouseRepairOutput) error {
	if err := writeTable(stdout, []string{"setting", "value"}, [][]string{
		{"endpoint", out.Endpoint},
		{"database", out.Database},
		{"needed", boolText(out.Plan.Needed)},
		{"dryRun", boolText(out.DryRun)},
		{"applied", fmt.Sprintf("%d", out.Applied)},
		{"table", out.Plan.Table},
		{"newTable", out.Plan.NewTable},
		{"backupTable", out.Plan.BackupTable},
		{"reason", out.Plan.Reason},
	}); err != nil {
		return err
	}
	if len(out.Plan.Statements) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	rows := make([][]string, 0, len(out.Plan.Statements))
	for i, statement := range out.Plan.Statements {
		rows = append(rows, []string{fmt.Sprintf("%d", i+1), strings.TrimSpace(statement)})
	}
	return writeTable(stdout, []string{"#", "statement"}, rows)
}

func writeClickHouseInitTable(stdout io.Writer, out clickHouseInitOutput) error {
	rows := [][]string{
		{"endpoint", out.Endpoint},
		{"database", out.Database},
		{"statements", fmt.Sprintf("%d", out.Statements)},
		{"applied", fmt.Sprintf("%d", out.Applied)},
	}
	return writeTable(stdout, []string{"setting", "value"}, rows)
}

func writeClickHouseImportTable(stdout io.Writer, out clickHouseImportOutput) error {
	rows := [][]string{
		{"endpoint", out.Endpoint},
		{"database", out.Database},
		{"rows", fmt.Sprintf("%d", out.Rows)},
	}
	for _, table := range []string{"observations", "object_aliases", "versions", "facts", "edges", "changes"} {
		if count := out.Tables[table]; count > 0 {
			rows = append(rows, []string{"table." + table, fmt.Sprintf("%d", count)})
		}
	}
	return writeTable(stdout, []string{"setting", "value"}, rows)
}
