package plugin

import (
	"context"
	"fmt"
	"io"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Plugin is what every plugin implements, in-process or behind Serve.
type Plugin interface {
	// Handshake declares the plugin, its IR version range and its features.
	Handshake() *irv1.PluginHandshake
	// Invoke does the plugin's work on one bundle.
	Invoke(ctx context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error)
}

// RefusedError reports that the core refused the plugin's handshake.
type RefusedError struct {
	Reason string
}

func (e *RefusedError) Error() string { return "plugin: refused by the core: " + e.Reason }

// Serve runs p over the stdio protocol: it answers the core's hello with p's
// handshake, then serves invocations until a shutdown message or the end of r.
func Serve(ctx context.Context, p Plugin, r io.Reader, w io.Writer) error {
	var msg irv1.CoreMessage
	if err := ReadMessage(r, &msg); err != nil {
		return err
	}
	if msg.GetHello() == nil {
		return fmt.Errorf("plugin: expected hello, got %T", msg.GetKind())
	}
	if err := WriteMessage(w, &irv1.PluginMessage{Kind: &irv1.PluginMessage_Handshake{Handshake: p.Handshake()}}); err != nil {
		return err
	}
	if err := ReadMessage(r, &msg); err != nil {
		return err
	}
	verdict := msg.GetHandshakeResult()
	if verdict == nil {
		return fmt.Errorf("plugin: expected handshake result, got %T", msg.GetKind())
	}
	if !verdict.GetAccepted() {
		return &RefusedError{Reason: verdict.GetReason()}
	}
	for {
		if err := ReadMessage(r, &msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		switch k := msg.GetKind().(type) {
		case *irv1.CoreMessage_Invoke:
			out := &irv1.PluginMessage{}
			if res, err := p.Invoke(ctx, k.Invoke); err != nil {
				out.Kind = &irv1.PluginMessage_Error{Error: &irv1.PluginError{Message: err.Error()}}
			} else {
				out.Kind = &irv1.PluginMessage_Result{Result: res}
			}
			if err := WriteMessage(w, out); err != nil {
				return err
			}
		case *irv1.CoreMessage_Shutdown:
			return nil
		default:
			return fmt.Errorf("plugin: unexpected %T after the handshake", k)
		}
	}
}
