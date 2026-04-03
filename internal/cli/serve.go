package cli

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/garett/aiprod/internal/agents"
	"github.com/garett/aiprod/internal/api"
	"github.com/garett/aiprod/internal/auth"
	"github.com/garett/aiprod/internal/db"
	"github.com/garett/aiprod/internal/docs"
	"github.com/garett/aiprod/internal/email"
	"github.com/garett/aiprod/internal/governor"
	"github.com/garett/aiprod/internal/knowledge"
	"github.com/garett/aiprod/internal/llm"
	"github.com/garett/aiprod/internal/memory"
	"github.com/garett/aiprod/internal/observe"
	"github.com/garett/aiprod/internal/planner"
	"github.com/garett/aiprod/internal/search"
	"github.com/garett/aiprod/internal/storage"
	"github.com/garett/aiprod/internal/tables"
	"github.com/garett/aiprod/internal/taskgraph"
	"github.com/garett/aiprod/internal/tasks"
	"github.com/garett/aiprod/internal/tools"
	"github.com/garett/aiprod/internal/webhooks"
	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cfg.EnsureDirectories(); err != nil {
				return err
			}

			// Open auth store (core.db)
			authStore, err := auth.NewStore(cfg.CoreDBPath())
			if err != nil {
				return fmt.Errorf("opening auth store: %w", err)
			}
			defer authStore.Close()
			coreDB := authStore.DB()

			// Initialize module stores
			fileStore, err := storage.NewStore(coreDB, cfg.FilesDir())
			if err != nil {
				return fmt.Errorf("opening file store: %w", err)
			}

			docStore, err := docs.NewStore(coreDB, cfg.DocsDir())
			if err != nil {
				return fmt.Errorf("opening docs store: %w", err)
			}

			tableStore, err := tables.NewStore(coreDB, cfg.TablesDBPath())
			if err != nil {
				return fmt.Errorf("opening tables store: %w", err)
			}
			defer tableStore.Close()

			taskStore, err := tasks.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening tasks store: %w", err)
			}

			// Email uses its own database
			emailDB, err := db.Open(cfg.EmailDBPath())
			if err != nil {
				return fmt.Errorf("opening email db: %w", err)
			}
			defer emailDB.Close()

			emailStore, err := email.NewStore(emailDB, cfg.EmailRawDir())
			if err != nil {
				return fmt.Errorf("opening email store: %w", err)
			}

			// Email: use mailr relay if configured, otherwise standalone SMTP
			var emailSender email.Sender
			var smtpServer *email.SMTPServer
			var mailrClient *email.MailrClient
			mailrURL := os.Getenv("AIPROD_MAILR_URL")
			if mailrURL != "" {
				mailrClient = email.NewMailrClient(
					emailStore,
					mailrURL,
					os.Getenv("AIPROD_MAILR_DOMAIN_ID"),
					os.Getenv("AIPROD_MAILR_AUTH_TOKEN"),
				)
				emailSender = mailrClient
				// Auto-register agent email address if configured
				if mailrAddr := os.Getenv("AIPROD_MAILR_ADDRESS"); mailrAddr != "" {
					localPart := strings.Split(mailrAddr, "@")[0]
					label := os.Getenv("AGENT_ID")
					if label == "" {
						label = localPart
					}
					if err := mailrClient.RegisterAddress(localPart, label); err != nil {
						fmt.Fprintf(os.Stderr, "mailr: register %s: %v (may already exist)\n", mailrAddr, err)
					} else {
						fmt.Printf("  Email:    %s (registered)\n", mailrAddr)
					}
				}
			} else {
				emailClient := email.NewSMTPClient(emailStore, cfg.Domain)
				smtpServer = email.NewSMTPServer(emailStore, cfg.Domain, cfg.SMTPAddr)
				emailSender = emailClient
				// Start outbound queue processor for standalone mode
				queueStop := make(chan struct{})
				go emailClient.StartQueueProcessor(queueStop)
				defer close(queueStop)
			}

			// Cognitive layer: memory, tools, governor, planner, taskgraph, agents, knowledge (core.db)
			memoryStore, err := memory.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening memory store: %w", err)
			}

			toolsStore, err := tools.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening tools store: %w", err)
			}

			governorStore, err := governor.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening governor store: %w", err)
			}

			plannerStore, err := planner.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening planner store: %w", err)
			}

			taskgraphStore, err := taskgraph.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening taskgraph store: %w", err)
			}

			agentsStore, err := agents.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening agents store: %w", err)
			}

			knowledgeStore, err := knowledge.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening knowledge store: %w", err)
			}

			webhooksStore, err := webhooks.NewStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening webhooks store: %w", err)
			}

			// Observe uses its own database (high write volume)
			observeDB, err := db.Open(cfg.ObserveDBPath())
			if err != nil {
				return fmt.Errorf("opening observe db: %w", err)
			}
			defer observeDB.Close()

			observeStore, err := observe.NewStore(observeDB)
			if err != nil {
				return fmt.Errorf("opening observe store: %w", err)
			}

			// Local LLM via Ollama
			llmClient := llm.NewClient()
			llmConfigStore, err := llm.NewConfigStore(coreDB)
			if err != nil {
				return fmt.Errorf("opening llm config store: %w", err)
			}
			if err := llmClient.Ping(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Ollama not available (%v) — /llm endpoints will return errors, semantic search disabled\n", err)
			} else {
				fmt.Printf("  LLM:      %s (%s)\n", llmClient.Model, llmClient.BaseURL)

				// Ensure embedding model is available (auto-pull on first run)
				embModel := llmClient.EmbeddingModel()
				if err := llmClient.EnsureModel(embModel); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: Could not ensure embedding model %s: %v\n", embModel, err)
				} else {
					fmt.Printf("  Embeddings: %s\n", embModel)
				}

				// Ensure default LLM model is available too
				if err := llmClient.EnsureModel(llmClient.Model); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: Could not ensure LLM model %s: %v\n", llmClient.Model, err)
				}

				memoryStore.SetLLM(llmClient)
			}

			// Unified search
			searchSvc := search.NewService(docStore, emailStore)

			// Create API server and register all routes
			srv := api.NewServer(authStore, cfg.NoAuth)
			srv.SetupV1Routes(func(r chi.Router) {
				// Core modules
				srv.RegisterFilesRoutes(r, fileStore)
				srv.RegisterDocsRoutes(r, docStore)
				srv.RegisterTablesRoutes(r, tableStore)
				srv.RegisterTasksRoutes(r, taskStore)
				srv.RegisterEmailRoutes(r, emailStore, emailSender)
				srv.RegisterSearchRoutes(r, searchSvc)
				// Cognitive layer
				srv.RegisterMemoryRoutes(r, memoryStore)
				srv.RegisterObserveRoutes(r, observeStore)
				srv.RegisterToolsRoutes(r, toolsStore)
				srv.RegisterGovernorRoutes(r, governorStore)
				srv.RegisterPlannerRoutes(r, plannerStore)
				srv.RegisterTaskGraphRoutes(r, taskgraphStore)
				srv.RegisterAgentsRoutes(r, agentsStore)
				srv.RegisterKnowledgeRoutes(r, knowledgeStore)
				srv.RegisterWebhooksRoutes(r, webhooksStore)
				// LLM-powered endpoints
				srv.RegisterLLMRoutes(r, &api.LLMStores{
					LLM:       llmClient,
					Config:    llmConfigStore,
					Memory:    memoryStore,
					Observe:   observeStore,
					Knowledge: knowledgeStore,
					Planner:   plannerStore,
				})
			})

			// Start webhook listeners for active subscriptions
			webhooksStore.StartAllListeners()

			// Start email services
			mailrStop := make(chan struct{})
			if mailrClient != nil {
				go mailrClient.StartPollProcessor(mailrStop)
				fmt.Printf("  Email:    mailr relay (%s)\n", mailrURL)
			} else if smtpServer != nil {
				go func() {
					if err := smtpServer.ListenAndServe(); err != nil {
						fmt.Fprintf(os.Stderr, "SMTP server error: %v\n", err)
					}
				}()
			}

			fmt.Printf("aiprod serving:\n")
			fmt.Printf("  HTTP API: %s\n", cfg.HTTPAddr)
			if smtpServer != nil {
				fmt.Printf("  SMTP:     %s\n", cfg.SMTPAddr)
			}
			fmt.Printf("  Domain:   %s\n", cfg.Domain)
			fmt.Printf("  Auth:     %v\n", !cfg.NoAuth)

			go func() {
				if err := http.ListenAndServe(cfg.HTTPAddr, srv.Router); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
					os.Exit(1)
				}
			}()

			// Start nightly dream cycle (memory consolidation)
			dreamStop := make(chan struct{})
			if memoryStore != nil {
				go func() {
					// Calculate time until next 3 AM UTC
					now := time.Now().UTC()
					next3AM := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
					if now.After(next3AM) {
						next3AM = next3AM.Add(24 * time.Hour)
					}
					delay := next3AM.Sub(now)
					fmt.Printf("  Dream:    next cycle at %s (in %s)\n", next3AM.Format("15:04 UTC"), delay.Round(time.Minute))

					timer := time.NewTimer(delay)
					for {
						select {
						case <-dreamStop:
							timer.Stop()
							return
						case <-timer.C:
							fmt.Println("[dream] Nightly dream cycle starting (per-agent)...")
							agentIDs, err := memoryStore.DistinctAgentIDs()
							if err != nil {
								fmt.Printf("[dream] Error listing agents: %v\n", err)
							} else {
								for _, agentID := range agentIDs {
									result, err := memoryStore.Dream(agentID)
									if err != nil {
										fmt.Printf("[dream] Error for %s: %v\n", agentID, err)
									} else {
										fmt.Printf("[dream] %s complete: expired=%d decayed=%d consolidated=%d total=%d\n",
											agentID, result.Expired, result.Decayed, result.Consolidated, result.TotalMemories)
									}
								}
							}
							timer.Reset(24 * time.Hour)
						}
					}
				}()
			}

			// Wait for interrupt
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			<-quit
			fmt.Println("\nShutting down...")
			close(dreamStop)
			webhooksStore.StopAllListeners()
			close(mailrStop)
			if smtpServer != nil {
				smtpServer.Close()
			}
			return nil
		},
	}
}
