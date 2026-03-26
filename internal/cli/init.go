package cli

import (
	"fmt"

	"github.com/garett/aiprod/internal/agents"
	"github.com/garett/aiprod/internal/auth"
	"github.com/garett/aiprod/internal/db"
	"github.com/garett/aiprod/internal/docs"
	"github.com/garett/aiprod/internal/governor"
	"github.com/garett/aiprod/internal/knowledge"
	"github.com/garett/aiprod/internal/llm"
	"github.com/garett/aiprod/internal/memory"
	"github.com/garett/aiprod/internal/observe"
	"github.com/garett/aiprod/internal/planner"
	"github.com/garett/aiprod/internal/storage"
	"github.com/garett/aiprod/internal/tables"
	"github.com/garett/aiprod/internal/taskgraph"
	"github.com/garett/aiprod/internal/tasks"
	"github.com/garett/aiprod/internal/tools"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize data directory and databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Initializing aiprod data directory: %s\n", cfg.DataDir)

			if err := cfg.EnsureDirectories(); err != nil {
				return fmt.Errorf("creating directories: %w", err)
			}
			fmt.Println("  + Directories created")

			// Initialize core database (auth tables)
			authStore, err := auth.NewStore(cfg.CoreDBPath())
			if err != nil {
				return fmt.Errorf("initializing core database: %w", err)
			}
			coreDB := authStore.DB()
			fmt.Println("  + Auth tables initialized")

			// Initialize storage tables
			if _, err := storage.NewStore(coreDB, cfg.FilesDir()); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing storage: %w", err)
			}
			fmt.Println("  + File storage initialized")

			// Initialize docs tables
			if _, err := docs.NewStore(coreDB, cfg.DocsDir()); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing docs: %w", err)
			}
			fmt.Println("  + Documents initialized")

			// Initialize tables module
			tableStore, err := tables.NewStore(coreDB, cfg.TablesDBPath())
			if err != nil {
				authStore.Close()
				return fmt.Errorf("initializing tables: %w", err)
			}
			tableStore.Close()
			fmt.Println("  + Data tables initialized")

			// Initialize tasks tables
			if _, err := tasks.NewStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing tasks: %w", err)
			}
			fmt.Println("  + Tasks initialized")

			// Initialize cognitive layer (core.db)
			if _, err := memory.NewStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing memory: %w", err)
			}
			fmt.Println("  + Memory initialized")

			if _, err := tools.NewStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing tools: %w", err)
			}
			fmt.Println("  + Tools initialized")

			if _, err := governor.NewStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing governor: %w", err)
			}
			fmt.Println("  + Governor initialized")

			if _, err := planner.NewStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing planner: %w", err)
			}
			fmt.Println("  + Planner initialized")

			if _, err := taskgraph.NewStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing taskgraph: %w", err)
			}
			fmt.Println("  + Task graph initialized")

			if _, err := agents.NewStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing agents: %w", err)
			}
			fmt.Println("  + Agents initialized")

			if _, err := knowledge.NewStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing knowledge: %w", err)
			}
			fmt.Println("  + Knowledge initialized")

			if _, err := llm.NewConfigStore(coreDB); err != nil {
				authStore.Close()
				return fmt.Errorf("initializing llm config: %w", err)
			}
			fmt.Println("  + LLM config initialized")

			// Initialize observe database (separate DB)
			observeDB, err := db.Open(cfg.ObserveDBPath())
			if err != nil {
				authStore.Close()
				return fmt.Errorf("opening observe db: %w", err)
			}
			if _, err := observe.NewStore(observeDB); err != nil {
				observeDB.Close()
				authStore.Close()
				return fmt.Errorf("initializing observe: %w", err)
			}
			observeDB.Close()
			fmt.Println("  + Observe initialized")

			authStore.Close()
			fmt.Println("\nInitialization complete. Run 'aiprod serve' to start.")
			return nil
		},
	}
}
