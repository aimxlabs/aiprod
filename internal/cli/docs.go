package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/garett/aiprod/internal/auth"
	"github.com/garett/aiprod/internal/docs"
	"github.com/spf13/cobra"
)

func newDocsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Manage documents",
	}

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a document (reads content from stdin)",
		RunE: func(cmd *cobra.Command, args []string) error {
			title, _ := cmd.Flags().GetString("title")
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			store, close, err := openDocsStore()
			if err != nil {
				return err
			}
			defer close()

			content, _ := io.ReadAll(os.Stdin)
			doc, err := store.Create(title, "agent:local", nil, string(content))
			if err != nil {
				return err
			}
			printJSON(doc)
			return nil
		},
	}
	create.Flags().String("title", "", "Document title")

	read := &cobra.Command{
		Use:   "read ID",
		Short: "Read a document (outputs markdown to stdout)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openDocsStore()
			if err != nil {
				return err
			}
			defer close()

			content, err := store.ReadContent(args[0])
			if err != nil {
				return err
			}
			fmt.Print(content)
			return nil
		},
	}

	write := &cobra.Command{
		Use:   "write ID",
		Short: "Update a document (reads new content from stdin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			msg, _ := cmd.Flags().GetString("message")
			store, close, err := openDocsStore()
			if err != nil {
				return err
			}
			defer close()

			content, _ := io.ReadAll(os.Stdin)
			doc, err := store.Update(args[0], "agent:local", msg, string(content))
			if err != nil {
				return err
			}
			printJSON(doc)
			return nil
		},
	}
	write.Flags().String("message", "", "Version message")

	list := &cobra.Command{
		Use:   "list",
		Short: "List documents",
		RunE: func(cmd *cobra.Command, args []string) error {
			tag, _ := cmd.Flags().GetString("tag")
			limitStr, _ := cmd.Flags().GetString("limit")
			limit, _ := strconv.Atoi(limitStr)
			store, close, err := openDocsStore()
			if err != nil {
				return err
			}
			defer close()

			result, err := store.List(docs.ListOptions{Tag: tag, Limit: limit})
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}
	list.Flags().String("tag", "", "Filter by tag")
	list.Flags().String("limit", "", "Max results")

	history := &cobra.Command{
		Use:   "history ID",
		Short: "Show version history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openDocsStore()
			if err != nil {
				return err
			}
			defer close()

			versions, err := store.ListVersions(args[0])
			if err != nil {
				return err
			}
			printJSON(versions)
			return nil
		},
	}

	search := &cobra.Command{
		Use:   "search QUERY",
		Short: "Search documents",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openDocsStore()
			if err != nil {
				return err
			}
			defer close()

			result, err := store.Search(args[0], 20)
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}

	cmd.AddCommand(create, read, write, list, history, search)
	return cmd
}

func openDocsStore() (*docs.Store, func(), error) {
	authStore, err := auth.NewStore(cfg.CoreDBPath())
	if err != nil {
		return nil, nil, err
	}
	store, err := docs.NewStore(authStore.DB(), cfg.DocsDir())
	if err != nil {
		authStore.Close()
		return nil, nil, err
	}
	return store, func() { authStore.Close() }, nil
}
