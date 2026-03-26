package llm

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/garett/aiprod/internal/db"
)

// Features that can have per-feature model overrides.
const (
	FeatureCompress      = "compress"
	FeatureExtractFacts  = "extract_facts"
	FeatureInferSchema   = "infer_schema"
	FeatureReflect       = "reflect"
	FeatureAnalyzeFailure = "analyze_failure"
)

var AllFeatures = []string{
	FeatureCompress,
	FeatureExtractFacts,
	FeatureInferSchema,
	FeatureReflect,
	FeatureAnalyzeFailure,
}

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS llm_config (
		feature     TEXT PRIMARY KEY,
		model       TEXT NOT NULL,
		temperature REAL DEFAULT -1,
		max_tokens  INTEGER DEFAULT -1,
		modified_at TEXT NOT NULL
	);`,
}

type ConfigStore struct{ db *sql.DB }

type FeatureConfig struct {
	Feature     string  `json:"feature"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	ModifiedAt  string  `json:"modified_at"`
}

func NewConfigStore(coreDB *sql.DB) (*ConfigStore, error) {
	if err := db.Migrate(coreDB, "llm_config", migrations); err != nil {
		return nil, fmt.Errorf("migrating llm_config schema: %w", err)
	}
	return &ConfigStore{db: coreDB}, nil
}

// SetFeatureModel sets the model override for a specific feature.
func (cs *ConfigStore) SetFeatureConfig(feature, model string, temperature float64, maxTokens int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := cs.db.Exec(
		`INSERT INTO llm_config (feature, model, temperature, max_tokens, modified_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(feature) DO UPDATE SET model=excluded.model, temperature=excluded.temperature, max_tokens=excluded.max_tokens, modified_at=excluded.modified_at`,
		feature, model, temperature, maxTokens, now,
	)
	return err
}

// GetFeatureConfig returns the config for a specific feature, or nil if not set.
func (cs *ConfigStore) GetFeatureConfig(feature string) (*FeatureConfig, error) {
	fc := &FeatureConfig{}
	err := cs.db.QueryRow(
		"SELECT feature, model, temperature, max_tokens, modified_at FROM llm_config WHERE feature = ?", feature,
	).Scan(&fc.Feature, &fc.Model, &fc.Temperature, &fc.MaxTokens, &fc.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	return fc, nil
}

// ListConfig returns all per-feature overrides.
func (cs *ConfigStore) ListConfig() ([]FeatureConfig, error) {
	rows, err := cs.db.Query("SELECT feature, model, temperature, max_tokens, modified_at FROM llm_config ORDER BY feature")
	if err != nil { return nil, err }
	defer rows.Close()
	var result []FeatureConfig
	for rows.Next() {
		var fc FeatureConfig
		rows.Scan(&fc.Feature, &fc.Model, &fc.Temperature, &fc.MaxTokens, &fc.ModifiedAt)
		result = append(result, fc)
	}
	return result, rows.Err()
}

// DeleteFeatureConfig removes the override for a feature (falls back to default).
func (cs *ConfigStore) DeleteFeatureConfig(feature string) error {
	_, err := cs.db.Exec("DELETE FROM llm_config WHERE feature = ?", feature)
	return err
}

// FullConfig returns the effective config for every feature, merging DB overrides with client defaults.
func (cs *ConfigStore) FullConfig(client *Client) (map[string]EffectiveConfig, error) {
	overrides, err := cs.ListConfig()
	if err != nil { return nil, err }

	overrideMap := make(map[string]*FeatureConfig)
	for i := range overrides {
		overrideMap[overrides[i].Feature] = &overrides[i]
	}

	result := make(map[string]EffectiveConfig)
	for _, f := range AllFeatures {
		ec := EffectiveConfig{
			Feature: f,
			Model:   client.Model,
			Source:  "default",
		}
		if ov, ok := overrideMap[f]; ok {
			ec.Model = ov.Model
			ec.Source = "config"
			if ov.Temperature >= 0 { ec.Temperature = &ov.Temperature }
			if ov.MaxTokens > 0 { ec.MaxTokens = &ov.MaxTokens }
		}
		result[f] = ec
	}
	return result, nil
}

type EffectiveConfig struct {
	Feature     string   `json:"feature"`
	Model       string   `json:"model"`
	Source      string   `json:"source"`
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
}

func (ec EffectiveConfig) MarshalJSON() ([]byte, error) {
	type Alias EffectiveConfig
	return json.Marshal(struct{ Alias }{Alias(ec)})
}
