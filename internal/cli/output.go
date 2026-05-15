package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

const (
	outputTable = "table"
	outputJSON  = "json"
)

func addOutputFlag(cmd *cobra.Command, target *string, defaultValue string) {
	*target = defaultValue
	cmd.Flags().StringVar(target, "output", defaultValue, "Output format: table or json")
}

func validateOutputFormat(format string) error {
	switch format {
	case outputTable, outputJSON:
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; use table or json", format)
	}
}

func outputFormatForCommand(cmd *cobra.Command, defaultValue string) (string, error) {
	output := defaultValue
	if flag := cmd.Flags().Lookup("output"); flag != nil {
		output = flag.Value.String()
	}
	if err := validateOutputFormat(output); err != nil {
		return "", err
	}
	return output, nil
}

func writeTable(stdout io.Writer, headers []string, rows [][]string) error {
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, strings.Join(tableCells(headers), "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(w, strings.Join(tableCells(row), "\t")); err != nil {
			return err
		}
	}
	return w.Flush()
}

func writeSection(stdout io.Writer, title string, headers []string, rows [][]string) error {
	if title != "" {
		if _, err := fmt.Fprintln(stdout, title); err != nil {
			return err
		}
	}
	return writeTable(stdout, headers, rows)
}

func humanBytes(n int64) string {
	if n < 0 {
		return "-" + humanBytes(-n)
	}
	return humanize.Bytes(uint64(n))
}

func humanCount(n int64) string {
	return humanize.Comma(n)
}

func humanDuration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	return end.Sub(start).Round(time.Millisecond).String()
}

func humanTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func humanOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return humanTime(*value)
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func compactResourceName(group, version, resource string) string {
	apiVersion := version
	if group != "" {
		apiVersion = group + "/" + version
	}
	return strings.Trim(apiVersion+"/"+resource, "/")
}

func shortText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 1 {
		return value[:limit]
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func tableCells(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(value)
	}
	return out
}
