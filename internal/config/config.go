package config

import (
	"encoding/json"
	"os"
	"news-aggregator/internal/models"
)

const configPath = "config/config.json"

func Load() (*models.Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &models.Config{
				Sheets: models.SheetsConfig{
					SheetName: "news",
				},
			}, nil
		}
		return nil, err
	}

	var cfg models.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func Save(cfg *models.Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll("config", 0755); err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Load() (*models.Config, error) {
	return Load()
}

func (m *Manager) Save(cfg *models.Config) error {
	return Save(cfg)
}
