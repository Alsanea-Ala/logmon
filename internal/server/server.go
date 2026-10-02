package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

const (
	handshakeTimeout = 5 * time.Second
	idleTimeout      = 15 * time.Minute
)

type AllowedAgent struct {
	TokenSHA256 string `yaml:"token_sha256"`
}

type Config struct {
	Listen         string                  `yaml:"listen"`
	DataDir        string                  `yaml:"data_dir"`
	MaxConnections int                     `yaml:"max_connections"`
	Agents         map[string]AllowedAgent `yaml:"agents"`
}

type agentStatus struct {
	ID        string
	Hostname  string
	Apps      []string
	Connected bool
	LastSeen  time.Time
	conn      net.Conn
}

type runtime struct {
	config  Config
	root    *os.Root
	program *tea.Program
	slots   chan struct{}
	mu      sync.RWMutex
	storeMu sync.Mutex
	agents  map[string]agentStatus
	failed  map[string]error
	workers sync.WaitGroup
}

func DefaultConfig() Config {
	return Config{Listen: "127.0.0.1:9000", DataDir: "data", MaxConnections: 100}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
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
	if !filepath.IsAbs(cfg.DataDir) {
		cfg.DataDir = filepath.Join(filepath.Dir(path), cfg.DataDir)
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	if cfg.Listen == "" {
		return fmt.Errorf("listen address is required")
	}
	if cfg.DataDir == "" {
		return fmt.Errorf("data directory is required")
	}
	if cfg.MaxConnections < 1 {
		return fmt.Errorf("max connections must be positive")
	}
	if len(cfg.Agents) == 0 {
		return fmt.Errorf("at least one allowed agent is required")
	}
	for id, agent := range cfg.Agents {
		if !protocol.ValidName(id) {
			return fmt.Errorf("invalid allowed agent id %q", id)
		}
		decoded, err := hex.DecodeString(agent.TokenSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("agent %q token_sha256 must be a SHA-256 hex digest", id)
		}
	}
	return nil
}

func Run(ctx context.Context, cfg Config, noTUI bool) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	root, err := os.OpenRoot(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open data directory: %w", err)
	}
	defer root.Close()

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	run := &runtime{
		config: cfg,
		root:   root,
		slots:  make(chan struct{}, cfg.MaxConnections),
		agents: make(map[string]agentStatus),
	}

	serverErrors := make(chan error, 1)

	if noTUI {
		// headless mode: just serve, no TUI
		go func() {
			err := run.serve(ctx, listener)
			serverErrors <- err
			if err != nil {
				cancel()
			}
		}()
		go func() {
			<-ctx.Done()
			listener.Close()
		}()

		serverErr := <-serverErrors
		run.closeConnections()
		run.workers.Wait()
		if serverErr != nil && !errors.Is(serverErr, net.ErrClosed) {
			return serverErr
		}
		return nil
	}

	// TUI mode
	model := newModel(cfg.DataDir)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithoutSignalHandler())
	run.program = program
	go func() {
		err := run.serve(ctx, listener)
		serverErrors <- err
		if err != nil {
			program.Quit()
		}
	}()
	go func() {
		<-ctx.Done()
		listener.Close()
		program.Quit()
	}()

	_, tuiErr := program.Run()
	cancel()
	listener.Close()
	serverErr := <-serverErrors
	run.closeConnections()
	run.workers.Wait()
	if tuiErr != nil {
		return fmt.Errorf("run TUI: %w", tuiErr)
	}
	if serverErr != nil && !errors.Is(serverErr, net.ErrClosed) {
		return serverErr
	}
	return nil
}

func (run *runtime) serve(ctx context.Context, listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept connection: %w", err)
		}
		select {
		case run.slots <- struct{}{}:
			run.workers.Go(func() {
				defer func() { <-run.slots }()
				run.handle(conn)
			})
		default:
			conn.Close()
		}
	}
}

func (run *runtime) handle(conn net.Conn) {
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		// Keepalive tuning is best effort. A failure here degrades to the
		// OS default rather than breaking the connection.
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(time.Minute)
	}
	encoder := json.NewEncoder(conn)
	scanner := protocol.Scanner(conn)
	// If arming a deadline fails, the connection is already unusable and the
	// decode below fails on its own rather than blocking forever.
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))

	var hello protocol.Hello
	if err := protocol.Decode(scanner, &hello); err != nil || !run.authenticate(hello) {
		// A failed rejection ACK is not actionable: this handler is already
		// returning an error, and the agent's own timeout covers the read.
		_ = encoder.Encode(protocol.Ack{OK: false, Error: "authentication failed"})
		return
	}
	if err := encoder.Encode(protocol.Ack{OK: true}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	run.register(hello, conn)
	defer run.disconnect(hello.AgentID, conn)

	declaredApps := make(map[string]bool, len(hello.Apps))
	for _, app := range hello.Apps {
		declaredApps[app] = true
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(idleTimeout))
		var record protocol.Record
		if err := protocol.Decode(scanner, &record); err != nil {
			return
		}
		if err := validateRecord(record, declaredApps); err != nil {
			// Same as the rejection ACK above: the handler is already
			// returning, and nothing downstream reads the ACK.
			_ = encoder.Encode(protocol.Ack{OK: false, Error: err.Error()})
			return
		}
		stored := protocol.StoredRecord{
			ReceivedAt: time.Now().UTC(),
			SourceTime: record.SourceTime,
			AgentID:    hello.AgentID,
			Hostname:   hello.Hostname,
			Source:     record.Source,
			App:        record.App,
			Category:   record.Category,
			Line:       record.Line,
		}
		if err := run.append(stored); err != nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := encoder.Encode(protocol.Ack{OK: true}); err != nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Time{})
		run.seen(hello.AgentID, stored.ReceivedAt)
		run.notify(recordMsg(stored))
	}
}

func (run *runtime) authenticate(hello protocol.Hello) bool {
	if hello.Version != protocol.Version || !protocol.ValidName(hello.AgentID) || hello.Hostname == "" || len(hello.Hostname) > 255 {
		return false
	}
	for _, app := range hello.Apps {
		if !protocol.ValidName(app) {
			return false
		}
	}
	allowed, ok := run.config.Agents[hello.AgentID]
	if !ok {
		return false
	}
	expected, err := hex.DecodeString(allowed.TokenSHA256)
	if err != nil {
		return false
	}
	actual := sha256.Sum256([]byte(hello.Token))
	return subtle.ConstantTimeCompare(expected, actual[:]) == 1
}

func validateRecord(record protocol.Record, apps map[string]bool) error {
	if !apps[record.App] || !protocol.ValidName(record.App) {
		return fmt.Errorf("invalid app")
	}
	if record.Category == "" || len(record.Category) > 128 {
		return fmt.Errorf("invalid category")
	}
	if len(record.Line) > protocol.MaxLineSize {
		return fmt.Errorf("log line is too large")
	}
	switch record.Source {
	case "file", "journald", "docker":
	default:
		return fmt.Errorf("invalid source")
	}
	return nil
}

func (run *runtime) append(record protocol.StoredRecord) error {
	// ponytail: one storage lock fits the <50-agent MVP; use per-file writers if profiling shows contention.
	run.storeMu.Lock()
	defer run.storeMu.Unlock()

	directory := filepath.Join(record.ReceivedAt.Format(time.DateOnly), record.App)
	path := filepath.Join(directory, record.AgentID+".jsonl")
	if err := run.failed[path]; err != nil {
		return fmt.Errorf("storage file disabled after rollback failure: %w", err)
	}
	if err := run.root.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// ponytail: open per record; cache file handles if profiling shows filesystem overhead.
	file, err := run.root.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	var rollbackErr error
	if writeErr != nil {
		rollbackErr = file.Truncate(info.Size())
		if rollbackErr != nil {
			if run.failed == nil {
				run.failed = make(map[string]error)
			}
			run.failed[path] = rollbackErr
		}
	}
	closeErr := file.Close()
	return errors.Join(writeErr, rollbackErr, closeErr)
}

func (run *runtime) register(hello protocol.Hello, conn net.Conn) {
	now := time.Now().UTC()
	status := agentStatus{ID: hello.AgentID, Hostname: hello.Hostname, Apps: hello.Apps, Connected: true, LastSeen: now, conn: conn}
	run.mu.Lock()
	old := run.agents[hello.AgentID]
	run.agents[hello.AgentID] = status
	run.mu.Unlock()
	if old.conn != nil && old.conn != conn {
		old.conn.Close()
	}
	run.notify(agentMsg(status))
}

func (run *runtime) seen(id string, at time.Time) {
	run.mu.Lock()
	status := run.agents[id]
	status.LastSeen = at
	run.agents[id] = status
	run.mu.Unlock()
	run.notify(agentMsg(status))
}

func (run *runtime) disconnect(id string, conn net.Conn) {
	run.mu.Lock()
	status := run.agents[id]
	if status.conn != conn {
		run.mu.Unlock()
		return
	}
	status.Connected = false
	status.conn = nil
	status.LastSeen = time.Now().UTC()
	run.agents[id] = status
	run.mu.Unlock()
	run.notify(agentMsg(status))
}

func (run *runtime) notify(message tea.Msg) {
	if run.program != nil {
		run.program.Send(message)
	}
}

func (run *runtime) closeConnections() {
	run.mu.RLock()
	defer run.mu.RUnlock()
	for _, status := range run.agents {
		if status.conn != nil {
			status.conn.Close()
		}
	}
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
