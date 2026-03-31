package cli

import (
	"fmt"
	"strconv"

	"github.com/garett/aiprod/internal/auth"
	"github.com/garett/aiprod/internal/tasks"
	"github.com/spf13/cobra"
)

func newTasksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "Manage tasks and workflows",
	}

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			title, _ := cmd.Flags().GetString("title")
			desc, _ := cmd.Flags().GetString("description")
			priority, _ := cmd.Flags().GetString("priority")
			assignee, _ := cmd.Flags().GetString("assignee")
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			store, close, err := openTasksStore()
			if err != nil {
				return err
			}
			defer close()

			t, err := store.Create(title, desc, priority, assignee, "agent:local", "agent:local", "", "", nil, nil)
			if err != nil {
				return err
			}
			printJSON(t)
			return nil
		},
	}
	create.Flags().String("title", "", "Task title")
	create.Flags().String("description", "", "Task description")
	create.Flags().String("priority", "medium", "Priority (low|medium|high|critical)")
	create.Flags().String("assignee", "", "Assignee agent ID")

	list := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			status, _ := cmd.Flags().GetString("status")
			assignee, _ := cmd.Flags().GetString("assignee")
			limitStr, _ := cmd.Flags().GetString("limit")
			limit, _ := strconv.Atoi(limitStr)
			store, close, err := openTasksStore()
			if err != nil {
				return err
			}
			defer close()

			result, err := store.List(tasks.ListOptions{
				Status: status, Assignee: assignee, Limit: limit,
			})
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}
	list.Flags().String("status", "", "Filter by status")
	list.Flags().String("assignee", "", "Filter by assignee")
	list.Flags().String("limit", "", "Max results")

	get := &cobra.Command{
		Use:   "get ID",
		Short: "Get task details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openTasksStore()
			if err != nil {
				return err
			}
			defer close()

			t, err := store.Get(args[0])
			if err != nil {
				return err
			}
			if t == nil {
				return fmt.Errorf("task not found")
			}
			events, _ := store.GetEvents(args[0])
			printJSON(map[string]interface{}{"task": t, "events": events})
			return nil
		},
	}

	transition := &cobra.Command{
		Use:   "transition ID STATUS",
		Short: "Transition task to new status",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openTasksStore()
			if err != nil {
				return err
			}
			defer close()

			t, err := store.Transition(args[0], "agent:local", args[1])
			if err != nil {
				return err
			}
			printJSON(t)
			return nil
		},
	}

	comment := &cobra.Command{
		Use:   "comment ID MESSAGE",
		Short: "Add a comment to a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, close, err := openTasksStore()
			if err != nil {
				return err
			}
			defer close()

			if err := store.AddComment(args[0], "agent:local", args[1]); err != nil {
				return err
			}
			printJSON(map[string]string{"status": "commented"})
			return nil
		},
	}

	cmd.AddCommand(create, list, get, transition, comment)
	return cmd
}

func openTasksStore() (*tasks.Store, func(), error) {
	authStore, err := auth.NewStore(cfg.CoreDBPath())
	if err != nil {
		return nil, nil, err
	}
	store, err := tasks.NewStore(authStore.DB())
	if err != nil {
		authStore.Close()
		return nil, nil, err
	}
	return store, func() { authStore.Close() }, nil
}
