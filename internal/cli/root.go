package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/garett/aiprod/config"
	"github.com/spf13/cobra"
)

var (
	cfg        *config.Config
	outputJSON bool
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "aiprod",
		Short: "AI-native productivity suite",
		Long:  "A fully AI-agent-first productivity suite: email, docs, tables, files, tasks.",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cfg = config.Default()
		},
	}

	root.PersistentFlags().BoolVar(&outputJSON, "json", false, "Force JSON output")

	root.AddCommand(newInitCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newAuthCmd())
	root.AddCommand(newEmailCmd())
	root.AddCommand(newDocsCmd())
	root.AddCommand(newTablesCmd())
	root.AddCommand(newFilesCmd())
	root.AddCommand(newTasksCmd())

	return root
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]interface{}{"ok": true, "data": v})
}

func printError(msg string) {
	if outputJSON {
		json.NewEncoder(os.Stderr).Encode(map[string]interface{}{
			"ok":    false,
			"error": map[string]string{"message": msg},
		})
	} else {
		fmt.Fprintf(os.Stderr, "error: %s\n", msg)
	}
}
