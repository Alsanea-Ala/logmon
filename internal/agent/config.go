// internal/agent/config.go

package agent

import (
	"gopkg.in/yaml.v3" // archived pkg
	"os"
)

type Agent struct {
	ID string `yaml:"id"`
}

type ServerConfig struct {
	Address           string `yaml:"address"`
	Port              int    `yaml:"port"`
	Retry             bool   `yaml:"retry"`
	ReconnectInterval int    `yaml:"reconnect_interval"`
}

type LogFile struct {
	Path          string `yaml:"path"`
	Label         string `yaml:"label"`
	FromBeginning bool   `yaml:"from_beginning"`
}

type AgentConfig struct {
	Agent Agent `yaml:"agent"`
	// Server ServerConfig `yaml:"server"`
	Logs []LogFile `yaml:"logs"`
}

func defaultConfig() AgentConfig {
	var cfg AgentConfig
	cfg.Agent.ID = "auto"
	// cfg.Server.Address = "localhost"
	// cfg.Server.Port = 9000
	// cfg.Server.Retry = true
	// cfg.Server.ReconnectInterval = 5
	cfg.Logs = []LogFile{
		{Path: "app.log", Label: "mylabel", FromBeginning: true},
	}
	return cfg
}

func GenerateConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists, skip
	}

	data, err := yaml.Marshal(defaultConfig())
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func LoadConfig(configPath string) (AgentConfig, error) {
	var cfg AgentConfig
	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg, err
	}
	err = yaml.Unmarshal(data, &cfg)

	return cfg, err
}
