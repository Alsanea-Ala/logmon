package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	"github.com/nxadm/tail"
)

var errRejected = errors.New("server rejected request")

func Run(ctx context.Context, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	hostname := cfg.Hostname

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	records := make(chan protocol.Record, 256)
	errors := make(chan error, len(cfg.Sources)+1)
	var workers sync.WaitGroup

	for _, source := range cfg.Sources {
		workers.Go(func() {
			if err := collect(ctx, cfg, source, records); err != nil && ctx.Err() == nil {
				errors <- err
			}
		})
	}
	workers.Go(func() {
		if err := send(ctx, cfg, hostname, records); err != nil && ctx.Err() == nil {
			errors <- err
		}
	})

	select {
	case <-ctx.Done():
		cancel()
		workers.Wait()
		return nil
	case err := <-errors:
		cancel()
		workers.Wait()
		return err
	}
}

func collect(ctx context.Context, cfg Config, source Source, records chan<- protocol.Record) error {
	state := sourceState{}
	for {
		var err error
		switch source.Type {
		case "file":
			err = collectFile(ctx, source, &state, records)
		case "journald":
			err = collectJournal(ctx, source, &state, records)
		case "docker":
			err = collectDocker(ctx, source, &state, records)
		}
		if ctx.Err() != nil {
			return nil
		}
		if !cfg.Retry {
			return fmt.Errorf("collect %s/%s: %w", source.Type, source.App, err)
		}
		if err := wait(ctx, cfg.RetryDelay); err != nil {
			return nil
		}
	}
}

type sourceState struct {
	fileLocation  *tail.SeekInfo
	fileInfo      os.FileInfo
	journalCursor string
	dockerSince   *time.Time
}

func collectFile(ctx context.Context, source Source, state *sourceState, records chan<- protocol.Record) error {
	location := state.fileLocation
	info, statErr := os.Stat(source.Path)
	if location != nil && (statErr != nil || state.fileInfo == nil || !os.SameFile(info, state.fileInfo) || info.Size() < location.Offset) {
		location = &tail.SeekInfo{Offset: 0, Whence: io.SeekStart}
	}
	if location == nil {
		location = &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd}
		if source.FromBeginning {
			location.Whence = io.SeekStart
		}
	}
	t, err := tail.TailFile(source.Path, tail.Config{
		Follow:        true,
		ReOpen:        true,
		Location:      location,
		MaxLineSize:   protocol.MaxFrameSize,
		CompleteLines: true,
		Logger:        tail.DiscardingLogger,
	})
	if err != nil {
		return err
	}
	defer t.Cleanup()

	for {
		select {
		case <-ctx.Done():
			return t.Stop()
		case line, ok := <-t.Lines:
			if !ok {
				return fmt.Errorf("tail stopped")
			}
			if line.Err != nil {
				return line.Err
			}
			if err := emit(ctx, records, source, line.Text, nil); err != nil {
				return err
			}
			location := line.SeekInfo
			state.fileLocation = &location
			if line.Num == 1 || state.fileInfo == nil {
				state.fileInfo, _ = os.Stat(source.Path)
			}
		}
	}
}

func collectJournal(ctx context.Context, source Source, state *sourceState, records chan<- protocol.Record) error {
	args := []string{"--follow", "--output=json", "--unit=" + source.Unit}
	if state.journalCursor != "" {
		args = append(args, "--after-cursor="+state.journalCursor)
	} else {
		lines := "all"
		if !source.FromBeginning {
			lines = "0"
		}
		args = append(args, "--lines="+lines)
	}
	return collectCommand(ctx, source, state, records, "journalctl", args...)
}

func collectDocker(ctx context.Context, source Source, state *sourceState, records chan<- protocol.Record) error {
	args := []string{"logs", "--follow", "--timestamps"}
	if state.dockerSince != nil {
		args = append(args, "--since="+state.dockerSince.Format(time.RFC3339Nano))
	} else if !source.FromBeginning {
		args = append(args, "--tail=0")
	}
	return collectCommand(ctx, source, state, records, "docker", append(args, source.Container)...)
}

func collectCommand(ctx context.Context, source Source, state *sourceState, records chan<- protocol.Record, name string, args ...string) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		writer.Close()
		reader.Close()
		return err
	}
	waited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		writer.CloseWithError(err)
		waited <- err
	}()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), protocol.MaxFrameSize)
	for scanner.Scan() {
		line, sourceTime, cursor, err := parseCommandLine(source.Type, scanner.Text())
		if err != nil {
			cancel()
			<-waited
			return err
		}
		if err := emit(ctx, records, source, line, sourceTime); err != nil {
			cancel()
			<-waited
			return err
		}
		if cursor != "" {
			state.journalCursor = cursor
		}
		if source.Type == "docker" && sourceTime != nil {
			state.dockerSince = sourceTime
		}
	}
	scanErr := scanner.Err()
	cancel()
	waitErr := <-waited
	if ctx.Err() != nil {
		return nil
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		return waitErr
	}
	return fmt.Errorf("%s stopped", name)
}

func parseCommandLine(kind, line string) (string, *time.Time, string, error) {
	if kind == "docker" {
		stamp, text, found := strings.Cut(line, " ")
		if !found {
			return line, nil, "", nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return line, nil, "", nil
		}
		return text, &parsed, "", nil
	}

	var entry struct {
		Message  json.RawMessage `json:"MESSAGE"`
		Realtime string          `json:"__REALTIME_TIMESTAMP"`
		Cursor   string          `json:"__CURSOR"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return "", nil, "", fmt.Errorf("decode journal entry: %w", err)
	}
	var message string
	if err := json.Unmarshal(entry.Message, &message); err != nil {
		message = string(entry.Message)
	}
	if entry.Realtime == "" {
		return message, nil, entry.Cursor, nil
	}
	microseconds, err := strconv.ParseInt(entry.Realtime, 10, 64)
	if err != nil {
		return "", nil, "", fmt.Errorf("decode journal timestamp: %w", err)
	}
	stamp := time.UnixMicro(microseconds).UTC()
	return message, &stamp, entry.Cursor, nil
}

func emit(ctx context.Context, records chan<- protocol.Record, source Source, line string, sourceTime *time.Time) error {
	for {
		end := min(len(line), protocol.MaxLineSize)
		for end < len(line) && end > 0 && line[end]&0xc0 == 0x80 {
			end--
		}
		if end == 0 {
			end = min(len(line), protocol.MaxLineSize)
		}
		select {
		case records <- protocol.Record{Source: source.Type, App: source.App, Category: source.Category, Line: line[:end], SourceTime: sourceTime}:
			if end == len(line) {
				return nil
			}
			line = line[end:]
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type serverConnection struct {
	net.Conn
	encoder *json.Encoder
	scanner *bufio.Scanner
}

func send(ctx context.Context, cfg Config, hostname string, records <-chan protocol.Record) error {
	var connection *serverConnection
	var pending *protocol.Record

	for {
		if connection == nil {
			var err error
			connection, err = connect(ctx, cfg, hostname)
			if err != nil {
				if errors.Is(err, errRejected) || !cfg.Retry {
					return err
				}
				if err := wait(ctx, cfg.RetryDelay); err != nil {
					return nil
				}
				continue
			}
		}

		if pending == nil {
			select {
			case <-ctx.Done():
				connection.Close()
				return nil
			case record := <-records:
				pending = &record
			}
		}

		active := connection
		// Arming deadlines: if setting one fails the connection is already
		// unusable, and the read or write below fails on its own rather than
		// blocking forever. The force-unblock and clear calls likewise only
		// matter on a connection that is still alive.
		_ = active.SetDeadline(time.Now().Add(30 * time.Second))
		stopCancel := context.AfterFunc(ctx, func() { _ = active.SetDeadline(time.Now()) })
		if err := active.encoder.Encode(pending); err == nil {
			var ack protocol.Ack
			err = protocol.Decode(active.scanner, &ack)
			if err == nil && !ack.OK {
				active.Close()
				return fmt.Errorf("%w: %s", errRejected, ack.Error)
			}
			if err == nil {
				pending = nil
				stopCancel()
				_ = active.SetDeadline(time.Time{})
				continue
			}
		}
		stopCancel()

		connection.Close()
		connection = nil
		if ctx.Err() != nil {
			return nil
		}
		if !cfg.Retry {
			return fmt.Errorf("send record: connection failed")
		}
		if err := wait(ctx, cfg.RetryDelay); err != nil {
			return nil
		}
	}
}

func connect(ctx context.Context, cfg Config, hostname string) (*serverConnection, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", cfg.Server)
	if err != nil {
		return nil, err
	}
	connection := &serverConnection{Conn: conn, encoder: json.NewEncoder(conn), scanner: protocol.Scanner(conn)}
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))

	if err := connection.encoder.Encode(protocol.Hello{
		Version:  protocol.Version,
		AgentID:  cfg.ID,
		Hostname: hostname,
		Token:    cfg.Token,
		Apps:     apps(cfg.Sources),
	}); err != nil {
		connection.Close()
		return nil, err
	}
	var ack protocol.Ack
	if err := protocol.Decode(connection.scanner, &ack); err != nil {
		connection.Close()
		return nil, err
	}
	if !ack.OK {
		connection.Close()
		return nil, fmt.Errorf("%w: %s", errRejected, ack.Error)
	}
	_ = connection.SetDeadline(time.Time{})
	return connection, nil
}

func apps(sources []Source) []string {
	seen := make(map[string]bool, len(sources))
	result := make([]string, 0, len(sources))
	for _, source := range sources {
		if !seen[source.App] {
			seen[source.App] = true
			result = append(result, source.App)
		}
	}
	return result
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
