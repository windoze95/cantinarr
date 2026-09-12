// Package appletv controls explicitly paired household Apple TVs. The helper
// owns Companion; Go owns authentication, title access and encrypted storage.
package appletv

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"sync"
)

type Device struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Address    string `json:"address,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

type Command struct {
	Action      string `json:"action"`
	Address     string `json:"address,omitempty"`
	Identifier  string `json:"identifier,omitempty"`
	Credentials string `json:"credentials,omitempty"`
	MediaType   string `json:"media_type,omitempty"`
	TMDBID      int64  `json:"tmdb_id,omitempty"`
}

type Reply struct {
	State       string   `json:"state"`
	Code        string   `json:"code"`
	Scope       string   `json:"scope"`
	Devices     []Device `json:"devices"`
	Device      Device   `json:"device"`
	Credentials string   `json:"credentials"`
}

// Conversation is private to one operation. Pairing can wait for a PIN; open
// waits for an explicit commit after the server rechecks live authorization.
type Conversation interface {
	Receive() (Reply, error)
	Send(any) error
	Close()
}

type Runner interface {
	Available() bool
	Start(context.Context, Command) (Conversation, error)
}

type ProcessRunner struct{ Binary string }

func (p ProcessRunner) binary() string {
	if p.Binary != "" {
		return p.Binary
	}
	return "cantinarr-appletv-helper"
}

func (p ProcessRunner) Available() bool {
	_, err := exec.LookPath(p.binary())
	return err == nil
}

func (p ProcessRunner) Start(ctx context.Context, command Command) (Conversation, error) {
	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, p.binary())
	// Credentials and PINs never appear in arguments, environment or logs.
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, problem("unsupported")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		stdin.Close()
		return nil, problem("unsupported")
	}
	if err := cmd.Start(); err != nil {
		cancel()
		stdin.Close()
		return nil, problem("unsupported")
	}
	c := &process{ctx: childCtx, cmd: cmd, cancel: cancel, stdin: stdin, decoder: json.NewDecoder(io.LimitReader(stdout, 1<<20))}
	if err := c.Send(command); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

type process struct {
	ctx     context.Context
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	stdin   io.WriteCloser
	decoder *json.Decoder
	once    sync.Once
}

func (p *process) Receive() (Reply, error) {
	var reply Reply
	if err := p.decoder.Decode(&reply); err != nil {
		if p.ctx.Err() != nil {
			return reply, problem("timeout")
		}
		return reply, problem("unreachable")
	}
	if reply.State == "error" {
		return reply, problem(reply.Code)
	}
	return reply, nil
}

func (p *process) Send(value any) error {
	if err := json.NewEncoder(p.stdin).Encode(value); err != nil {
		return problem("unreachable")
	}
	return nil
}

func (p *process) Close() {
	p.once.Do(func() { p.cancel(); p.stdin.Close(); _ = p.cmd.Wait() })
}
