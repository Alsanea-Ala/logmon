package agent

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	"gopkg.in/yaml.v3"
)

type Source struct {
	Type          string `yaml:"type"`
	Path          string `yaml:"path,omitempty"`
	Unit          string `yaml:"unit,omitempty"`
	Container     string `yaml:"container,omitempty"`
	App           string `yaml:"app"`
	Category      string `yaml:"category"`
	FromBeginning bool   `yaml:"from_beginning"`
}

type Config struct {
	ID         string        `yaml:"id"`
	Hostname   string        `yaml:"hostname,omitempty"`
	Server     string        `yaml:"server"`
	Token      string        `yaml:"token,omitempty"`
	Retry      bool          `yaml:"retry"`
	RetryDelay time.Duration `yaml:"retry_delay"`
	Sources    []Source      `yaml:"sources"`
}

func DefaultConfig() Config {
	return Config{
		Server:     "127.0.0.1:9000",
		Retry:      true,
		RetryDelay: 2 * time.Second,
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		cfg.Token = os.Getenv("LOGMON_AGENT_TOKEN")
		return cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, err
	}

	for i := range cfg.Sources {
		if cfg.Sources[i].Type == "file" && !filepath.IsAbs(cfg.Sources[i].Path) {
			cfg.Sources[i].Path = filepath.Join(filepath.Dir(path), cfg.Sources[i].Path)
		}
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("LOGMON_AGENT_TOKEN")
	}
	return cfg, nil
}

func ParseSource(kind, spec string) (Source, error) {
	reader := csv.NewReader(strings.NewReader(spec))
	reader.FieldsPerRecord = 4
	fields, err := reader.Read()
	if err != nil {
		return Source{}, fmt.Errorf("parse %s source: %w", kind, err)
	}
	if _, err := reader.Read(); err != io.EOF {
		return Source{}, fmt.Errorf("parse %s source: expected one CSV record", kind)
	}

	fromBeginning, err := strconv.ParseBool(fields[3])
	if err != nil {
		return Source{}, fmt.Errorf("parse %s source from_beginning: %w", kind, err)
	}
	source := Source{Type: kind, App: fields[1], Category: fields[2], FromBeginning: fromBeginning}
	switch kind {
	case "file":
		source.Path = fields[0]
	case "journald":
		source.Unit = fields[0]
	case "docker":
		source.Container = fields[0]
	default:
		return Source{}, fmt.Errorf("unknown source type %q", kind)
	}
	return source, nil
}

func (cfg Config) Validate() error {
	if !protocol.ValidName(cfg.ID) {
		return fmt.Errorf("agent id must contain only letters, numbers, dot, dash, or underscore")
	}
	if cfg.Server == "" {
		return fmt.Errorf("server address is required")
	}
	if len(cfg.Token) < 32 {
		return fmt.Errorf("agent token must contain at least 32 characters")
	}
	if cfg.RetryDelay <= 0 {
		return fmt.Errorf("retry delay must be positive")
	}
	if len(cfg.Sources) == 0 {
		return fmt.Errorf("at least one source is required")
	}
	for i, source := range cfg.Sources {
		if !protocol.ValidName(source.App) {
			return fmt.Errorf("source %d app must contain only letters, numbers, dot, dash, or underscore", i+1)
		}
		if source.Category == "" || len(source.Category) > 128 {
			return fmt.Errorf("source %d category must contain 1-128 characters", i+1)
		}
		switch source.Type {
		case "file":
			if source.Path == "" {
				return fmt.Errorf("source %d file path is required", i+1)
			}
		case "journald":
			if source.Unit == "" {
				return fmt.Errorf("source %d journal unit is required", i+1)
			}
		case "docker":
			if source.Container == "" {
				return fmt.Errorf("source %d Docker container is required", i+1)
			}
		default:
			return fmt.Errorf("source %d has unknown type %q", i+1, source.Type)
		}
	}
	return nil
}
