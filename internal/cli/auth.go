package cli

import (
	"fmt"
	"strings"

	"github.com/garett/aiprod/internal/auth"
	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage agents and API keys",
	}

	// create-agent
	var agentDesc string
	createAgent := &cobra.Command{
		Use:   "create-agent --name NAME",
		Short: "Create a new agent identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			store, err := auth.NewStore(cfg.CoreDBPath())
			if err != nil {
				return err
			}
			defer store.Close()

			agent, err := store.CreateAgent(name, agentDesc)
			if err != nil {
				return err
			}
			printJSON(agent)
			return nil
		},
	}
	createAgent.Flags().String("name", "", "Agent name")
	createAgent.Flags().StringVar(&agentDesc, "description", "", "Agent description")

	// create-key
	createKey := &cobra.Command{
		Use:   "create-key --agent NAME --scopes SCOPES",
		Short: "Create an API key for an agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, _ := cmd.Flags().GetString("agent")
			scopesStr, _ := cmd.Flags().GetString("scopes")
			keyName, _ := cmd.Flags().GetString("name")
			expires, _ := cmd.Flags().GetString("expires")

			if agentName == "" || scopesStr == "" {
				return fmt.Errorf("--agent and --scopes are required")
			}

			store, err := auth.NewStore(cfg.CoreDBPath())
			if err != nil {
				return err
			}
			defer store.Close()

			agentID := "agent:" + agentName
			scopes := strings.Split(scopesStr, ",")

			key, rawKey, err := store.CreateAPIKey(agentID, keyName, scopes, expires)
			if err != nil {
				return err
			}

			printJSON(map[string]interface{}{
				"key":     key,
				"raw_key": rawKey,
			})
			fmt.Fprintf(cmd.ErrOrStderr(), "\n⚠ Save this key — it will not be shown again.\n")
			return nil
		},
	}
	createKey.Flags().String("agent", "", "Agent name")
	createKey.Flags().String("scopes", "", "Comma-separated scopes (e.g. docs:*,tasks:read)")
	createKey.Flags().String("name", "", "Key label")
	createKey.Flags().String("expires", "", "Expiry (RFC3339 timestamp)")

	// list-keys
	listKeys := &cobra.Command{
		Use:   "list-keys",
		Short: "List API keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentFilter, _ := cmd.Flags().GetString("agent")
			store, err := auth.NewStore(cfg.CoreDBPath())
			if err != nil {
				return err
			}
			defer store.Close()

			agentID := ""
			if agentFilter != "" {
				agentID = "agent:" + agentFilter
			}
			keys, err := store.ListKeys(agentID)
			if err != nil {
				return err
			}
			printJSON(keys)
			return nil
		},
	}
	listKeys.Flags().String("agent", "", "Filter by agent name")

	// revoke-key
	revokeKey := &cobra.Command{
		Use:   "revoke-key KEY_ID",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := auth.NewStore(cfg.CoreDBPath())
			if err != nil {
				return err
			}
			defer store.Close()

			if err := store.RevokeKey(args[0]); err != nil {
				return err
			}
			printJSON(map[string]string{"status": "revoked", "key_id": args[0]})
			return nil
		},
	}

	// list-agents
	listAgents := &cobra.Command{
		Use:   "list-agents",
		Short: "List all agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := auth.NewStore(cfg.CoreDBPath())
			if err != nil {
				return err
			}
			defer store.Close()

			agents, err := store.ListAgents()
			if err != nil {
				return err
			}
			printJSON(agents)
			return nil
		},
	}

	cmd.AddCommand(createAgent, createKey, listKeys, revokeKey, listAgents)
	return cmd
}
