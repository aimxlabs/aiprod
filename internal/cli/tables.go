package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/garett/aiprod/internal/auth"
	"github.com/garett/aiprod/internal/tables"
	"github.com/spf13/cobra"
)

func newTablesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tables",
		Short: "Manage data tables",
	}

	create := &cobra.Command{
		Use:   "create NAME --columns col:type,...",
		Short: "Create a new table",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			colsStr, _ := cmd.Flags().GetString("columns")
			desc, _ := cmd.Flags().GetString("description")
			if colsStr == "" {
				return fmt.Errorf("--columns is required (e.g. name:text,score:real)")
			}
			store, close, err := openTablesStore()
			if err != nil {
				return err
			}
			defer close()

			var columns []tables.Column
			for i, spec := range strings.Split(colsStr, ",") {
				parts := strings.SplitN(spec, ":", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid column spec: %s (expected name:type)", spec)
				}
				columns = append(columns, tables.Column{
					Name:     strings.TrimSpace(parts[0]),
					Type:     strings.TrimSpace(parts[1]),
					Position: i,
				})
			}

			t, err := store.Create(args[0], desc, columns)
			if err != nil {
				return err
			}
			printJSON(t)
			return nil
		},
	}
	create.Flags().String("columns", "", "Column definitions (name:type,...)")
	create.Flags().String("description", "", "Table description")

	list := &cobra.Command{
		Use:   "list",
		Short: "List tables",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openTablesStore()
			if err != nil {
				return err
			}
			defer close()

			result, err := store.List()
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}

	insert := &cobra.Command{
		Use:   "insert TABLE --data JSON",
		Short: "Insert a row",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dataStr, _ := cmd.Flags().GetString("data")
			store, close, err := openTablesStore()
			if err != nil {
				return err
			}
			defer close()

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
				return fmt.Errorf("invalid JSON data: %w", err)
			}
			rowID, err := store.InsertRow(args[0], data)
			if err != nil {
				return err
			}
			printJSON(map[string]interface{}{"_rowid": rowID})
			return nil
		},
	}
	insert.Flags().String("data", "", "Row data as JSON object")

	query := &cobra.Command{
		Use:   "query TABLE",
		Short: "Query table rows",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			where, _ := cmd.Flags().GetString("where")
			sort, _ := cmd.Flags().GetString("sort")
			limitStr, _ := cmd.Flags().GetString("limit")
			limit, _ := strconv.Atoi(limitStr)
			store, close, err := openTablesStore()
			if err != nil {
				return err
			}
			defer close()

			rows, err := store.QueryRows(args[0], where, sort, limit, 0)
			if err != nil {
				return err
			}
			printJSON(rows)
			return nil
		},
	}
	query.Flags().String("where", "", "WHERE clause")
	query.Flags().String("sort", "", "ORDER BY clause")
	query.Flags().String("limit", "", "Row limit")

	sqlCmd := &cobra.Command{
		Use:   "sql TABLE QUERY",
		Short: "Execute SQL query (SELECT only)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openTablesStore()
			if err != nil {
				return err
			}
			defer close()

			rows, err := store.ExecSQL(args[0], args[1])
			if err != nil {
				return err
			}
			printJSON(rows)
			return nil
		},
	}

	cmd.AddCommand(create, list, insert, query, sqlCmd)
	return cmd
}

func openTablesStore() (*tables.Store, func(), error) {
	authStore, err := auth.NewStore(cfg.CoreDBPath())
	if err != nil {
		return nil, nil, err
	}
	store, err := tables.NewStore(authStore.DB(), cfg.TablesDBPath())
	if err != nil {
		authStore.Close()
		return nil, nil, err
	}
	return store, func() {
		store.Close()
		authStore.Close()
	}, nil
}
