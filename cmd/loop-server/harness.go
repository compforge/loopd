package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	agent "github.com/compforge/agentgo"
	"github.com/compforge/agentgo/llm"
	"github.com/compforge/agentgo/tools"
	"github.com/compforge/loopd/pkg/harness"
	agentgo "github.com/compforge/loopd/pkg/harness/agentgo"
	"github.com/compforge/loopd/pkg/harness/managedagent"
)

// HARNESS_CONFIG_FILE names a deployment-owned JSON map. Secrets can be supplied
// through named environment variables; they are not copied into harness_runs.
type harnessConfig struct {
	Provider             string `json:"provider"`
	ModelProvider        string `json:"model_provider"`
	Model                string `json:"model"`
	APIKeyEnv            string `json:"api_key_env"`
	BaseURL              string `json:"base_url"`
	AgentID              string `json:"agent_id"`
	EnvironmentID        string `json:"environment_id"`
	IdempotentSubmission bool   `json:"idempotent_submission"`
	WorkspaceRoot        string `json:"workspace_root"`
	Tools                string `json:"tools"` // none, read, write; deployment-owned permissions
}

func loadHarnesses() (map[string]harness.Adapter, error) {
	result := map[string]harness.Adapter{}
	path := os.Getenv("HARNESS_CONFIG_FILE")
	if path == "" {
		return result, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var configs map[string]harnessConfig
	if err := json.Unmarshal(raw, &configs); err != nil {
		return nil, err
	}
	for key, cfg := range configs {
		if key == "" {
			return nil, fmt.Errorf("Harness key is required")
		}
		apiKey := os.Getenv(cfg.APIKeyEnv)
		switch cfg.Provider {
		case "managedagent":
			adapter, err := managedagent.New(managedagent.Config{APIKey: apiKey, BaseURL: cfg.BaseURL, AgentID: cfg.AgentID, EnvironmentID: cfg.EnvironmentID, IdempotentSubmission: cfg.IdempotentSubmission})
			if err != nil {
				return nil, fmt.Errorf("Harness %s: %w", key, err)
			}
			result[key] = adapter
		case "agentgo":
			if cfg.Tools != "" && cfg.Tools != "none" && cfg.Tools != "read" && cfg.Tools != "write" {
				return nil, fmt.Errorf("Harness %s: invalid tools mode", key)
			}
			if (cfg.Tools == "read" || cfg.Tools == "write") && cfg.WorkspaceRoot == "" {
				return nil, fmt.Errorf("Harness %s: workspace_root is required for file tools", key)
			}
			opts := []llm.ModelOption{llm.WithRequestTimeout(30 * time.Minute), llm.WithStreamIdleTimeout(2 * time.Minute)}
			if apiKey != "" {
				opts = append(opts, llm.WithAPIKey(apiKey))
			}
			if cfg.BaseURL != "" {
				opts = append(opts, llm.WithBaseURL(cfg.BaseURL))
			}
			model, err := llm.NewModel(cfg.ModelProvider, cfg.Model, opts...)
			if err != nil {
				return nil, fmt.Errorf("Harness %s: %w", key, err)
			}
			adapter, err := agentgo.New(func(ctx context.Context, request harness.Request) (*agent.Agent, error) {
				if len(request.Tools) > 0 {
					return nil, fmt.Errorf("agentgo demo uses deployment-configured tools")
				}
				var toolset []agent.Tool
				if cfg.Tools == "read" || cfg.Tools == "write" {
					dir, err := filepath.Abs(filepath.Join(cfg.WorkspaceRoot, fmt.Sprintf("%x", sha256.Sum256([]byte(request.ScopeKey)))))
					if err != nil {
						return nil, err
					}
					if err := os.MkdirAll(dir, 0700); err != nil {
						return nil, err
					}
					state := tools.NewFileReadState()
					toolset = []agent.Tool{tools.NewRead(dir, state), tools.NewLs(dir), tools.NewGlob(dir), tools.NewGrep(dir)}
					if cfg.Tools == "write" {
						toolset = append(toolset, tools.NewBash(dir), tools.NewWrite(dir, state), tools.NewEdit(dir, state))
					}
				}
				return agent.NewAgent(agent.WithModel(model), agent.WithTools(toolset...), agent.WithMaxTurns(32)), nil
			})
			if err != nil {
				return nil, err
			}
			result[key] = adapter
		default:
			return nil, fmt.Errorf("Harness %s: unknown provider %q", key, cfg.Provider)
		}
	}
	return result, nil
}
