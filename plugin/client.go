package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Client is the core's connection to one negotiated plugin.
type Client interface {
	// Handshake is what the plugin declared.
	Handshake() *irv1.PluginHandshake
	// Invoke hands the plugin a bundle and its settings, and returns its
	// result.
	Invoke(ctx context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error)
	// Close ends the session; for an executable it waits for it to exit.
	Close() error
}

// InProcess negotiates with p directly, without a process boundary.
func InProcess(p Plugin, hello *irv1.CoreHello, kind irv1.PluginKind) (Client, error) {
	hs := p.Handshake()
	if err := Negotiate(hello, hs, kind); err != nil {
		return nil, err
	}
	return &inProcess{p: p, hs: hs}, nil
}

type inProcess struct {
	p  Plugin
	hs *irv1.PluginHandshake
}

func (c *inProcess) Handshake() *irv1.PluginHandshake { return c.hs }

func (c *inProcess) Invoke(ctx context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	return c.p.Invoke(ctx, in)
}

func (c *inProcess) Close() error { return nil }

// Start runs the executable at path, speaks the stdio protocol with it and
// negotiates. A refused plugin is told why, and Start waits for it to exit.
func Start(ctx context.Context, path string, args []string, hello *irv1.CoreHello, kind irv1.PluginKind) (Client, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	stdin, _ := cmd.StdinPipe()   // cannot fail: Stdin is unset
	stdout, _ := cmd.StdoutPipe() // cannot fail: Stdout is unset
	c := &process{cmd: cmd, w: stdin, r: stdout}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("plugin: starting %s: %w", path, err)
	}
	if err := c.handshake(hello, kind); err != nil {
		return nil, c.stop(err)
	}
	return c, nil
}

type process struct {
	cmd    *exec.Cmd
	w      io.WriteCloser
	r      io.Reader
	stderr bytes.Buffer
	hs     *irv1.PluginHandshake
}

func (c *process) handshake(hello *irv1.CoreHello, kind irv1.PluginKind) error {
	if err := WriteMessage(c.w, &irv1.CoreMessage{Kind: &irv1.CoreMessage_Hello{Hello: hello}}); err != nil {
		return err
	}
	var msg irv1.PluginMessage
	if err := ReadMessage(c.r, &msg); err != nil {
		return err
	}
	c.hs = msg.GetHandshake()
	if c.hs == nil {
		return fmt.Errorf("plugin: expected a handshake, got %T", msg.GetKind())
	}
	refusal := Negotiate(hello, c.hs, kind)
	verdict := &irv1.HandshakeResult{Accepted: refusal == nil}
	if refusal != nil {
		verdict.Reason = refusal.Error()
	}
	if err := WriteMessage(c.w, &irv1.CoreMessage{Kind: &irv1.CoreMessage_HandshakeResult{HandshakeResult: verdict}}); err != nil {
		return err
	}
	return refusal
}

func (c *process) Handshake() *irv1.PluginHandshake { return c.hs }

func (c *process) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	if err := WriteMessage(c.w, &irv1.CoreMessage{Kind: &irv1.CoreMessage_Invoke{Invoke: in}}); err != nil {
		return nil, err
	}
	var msg irv1.PluginMessage
	if err := ReadMessage(c.r, &msg); err != nil {
		return nil, err
	}
	switch k := msg.GetKind().(type) {
	case *irv1.PluginMessage_Result:
		return k.Result, nil
	case *irv1.PluginMessage_Error:
		return nil, fmt.Errorf("plugin %s: %s", c.hs.GetPlugin().GetName(), k.Error.GetMessage())
	default:
		return nil, fmt.Errorf("plugin: expected a result, got %T", k)
	}
}

// Close sends shutdown and waits for the plugin to exit.
func (c *process) Close() error {
	err := WriteMessage(c.w, &irv1.CoreMessage{Kind: &irv1.CoreMessage_Shutdown{Shutdown: &irv1.Shutdown{}}})
	return c.stop(err)
}

// stop closes the plugin's stdin, waits for it, and joins err with any exit
// failure and the plugin's stderr.
func (c *process) stop(err error) error {
	c.w.Close()
	if werr := c.cmd.Wait(); werr != nil {
		err = errors.Join(err, fmt.Errorf("plugin: %s: %w", c.cmd.Path, werr))
	}
	if err != nil && c.stderr.Len() > 0 {
		err = fmt.Errorf("%w\nplugin stderr: %s", err, strings.TrimSpace(c.stderr.String()))
	}
	return err
}
