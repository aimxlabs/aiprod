package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/garett/aiprod/internal/auth"
	"github.com/garett/aiprod/internal/storage"
	"github.com/spf13/cobra"
)

func newFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Manage file storage",
	}

	upload := &cobra.Command{
		Use:   "upload FILE",
		Short: "Upload a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tagsStr, _ := cmd.Flags().GetString("tags")
			store, close, err := openFileStore()
			if err != nil {
				return err
			}
			defer close()

			f, err := os.Open(args[0])
			if err != nil {
				return fmt.Errorf("opening file: %w", err)
			}
			defer f.Close()

			var tags []string
			if tagsStr != "" {
				tags = strings.Split(tagsStr, ",")
			}

			result, err := store.Put(f.Name(), "", "agent:local", tags, nil, f)
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}
	upload.Flags().String("tags", "", "Comma-separated tags")

	list := &cobra.Command{
		Use:   "list",
		Short: "List files",
		RunE: func(cmd *cobra.Command, args []string) error {
			tag, _ := cmd.Flags().GetString("tag")
			limitStr, _ := cmd.Flags().GetString("limit")
			limit, _ := strconv.Atoi(limitStr)
			store, close, err := openFileStore()
			if err != nil {
				return err
			}
			defer close()

			result, err := store.List(storage.ListOptions{Tag: tag, Limit: limit})
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}
	list.Flags().String("tag", "", "Filter by tag")
	list.Flags().String("limit", "", "Max results")

	download := &cobra.Command{
		Use:   "download ID",
		Short: "Download a file to stdout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openFileStore()
			if err != nil {
				return err
			}
			defer close()

			reader, _, err := store.Open(args[0])
			if err != nil {
				return err
			}
			if reader == nil {
				return fmt.Errorf("file not found")
			}
			defer reader.Close()
			io.Copy(os.Stdout, reader)
			return nil
		},
	}

	cmd.AddCommand(upload, list, download)
	return cmd
}

func openFileStore() (*storage.Store, func(), error) {
	authStore, err := auth.NewStore(cfg.CoreDBPath())
	if err != nil {
		return nil, nil, err
	}
	store, err := storage.NewStore(authStore.DB(), cfg.FilesDir())
	if err != nil {
		authStore.Close()
		return nil, nil, err
	}
	return store, func() { authStore.Close() }, nil
}
