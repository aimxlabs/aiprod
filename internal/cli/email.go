package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/garett/aiprod/internal/db"
	"github.com/garett/aiprod/internal/email"
	"github.com/spf13/cobra"
)

func newEmailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email",
		Short: "Manage email",
	}

	send := &cobra.Command{
		Use:   "send",
		Short: "Send an email",
		RunE: func(cmd *cobra.Command, args []string) error {
			to, _ := cmd.Flags().GetString("to")
			subject, _ := cmd.Flags().GetString("subject")
			body, _ := cmd.Flags().GetString("body")
			from, _ := cmd.Flags().GetString("from")

			if to == "" || subject == "" {
				return fmt.Errorf("--to and --subject are required")
			}

			_, client, close, err := openEmailService()
			if err != nil {
				return err
			}
			defer close()

			toAddrs := strings.Split(to, ",")
			msg, err := client.Send(from, toAddrs, nil, subject, body, "")
			if err != nil {
				return err
			}
			printJSON(msg)
			return nil
		},
	}
	send.Flags().String("to", "", "Recipient (comma-separated)")
	send.Flags().String("subject", "", "Subject line")
	send.Flags().String("body", "", "Message body")
	send.Flags().String("from", "", "From address (default: noreply@domain)")

	list := &cobra.Command{
		Use:   "list",
		Short: "List messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			label, _ := cmd.Flags().GetString("label")
			limitStr, _ := cmd.Flags().GetString("limit")
			limit, _ := strconv.Atoi(limitStr)

			store, _, close, err := openEmailService()
			if err != nil {
				return err
			}
			defer close()

			msgs, err := store.List(email.ListOptions{Label: label, Limit: limit})
			if err != nil {
				return err
			}
			printJSON(msgs)
			return nil
		},
	}
	list.Flags().String("label", "", "Filter by label")
	list.Flags().String("limit", "20", "Max results")

	get := &cobra.Command{
		Use:   "get ID",
		Short: "Get a message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, close, err := openEmailService()
			if err != nil {
				return err
			}
			defer close()

			msg, err := store.Get(args[0])
			if err != nil {
				return err
			}
			if msg == nil {
				return fmt.Errorf("message not found")
			}
			printJSON(msg)
			return nil
		},
	}

	search := &cobra.Command{
		Use:   "search QUERY",
		Short: "Search messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, close, err := openEmailService()
			if err != nil {
				return err
			}
			defer close()

			msgs, err := store.Search(args[0], 20)
			if err != nil {
				return err
			}
			printJSON(msgs)
			return nil
		},
	}

	label := &cobra.Command{
		Use:   "label ID",
		Short: "Add/remove labels",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			add, _ := cmd.Flags().GetString("add")
			remove, _ := cmd.Flags().GetString("remove")

			store, _, close, err := openEmailService()
			if err != nil {
				return err
			}
			defer close()

			if add != "" {
				for _, l := range strings.Split(add, ",") {
					store.AddLabel(args[0], strings.TrimSpace(l))
				}
			}
			if remove != "" {
				for _, l := range strings.Split(remove, ",") {
					store.RemoveLabel(args[0], strings.TrimSpace(l))
				}
			}

			msg, _ := store.Get(args[0])
			printJSON(msg)
			return nil
		},
	}
	label.Flags().String("add", "", "Labels to add (comma-separated)")
	label.Flags().String("remove", "", "Labels to remove (comma-separated)")

	cmd.AddCommand(send, list, get, search, label)
	return cmd
}

func openEmailService() (*email.Store, *email.SMTPClient, func(), error) {
	emailDB, err := db.Open(cfg.EmailDBPath())
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := email.NewStore(emailDB, cfg.EmailRawDir())
	if err != nil {
		emailDB.Close()
		return nil, nil, nil, err
	}
	client := email.NewSMTPClient(store, cfg.Domain)
	return store, client, func() { emailDB.Close() }, nil
}
