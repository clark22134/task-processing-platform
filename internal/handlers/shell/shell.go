package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type Shell struct{}

type Cmd struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func (h Shell) Handle(ctx context.Context, payload json.RawMessage) error {
	var c Cmd
	if err := json.Unmarshal(payload, &c); err != nil {
		return err
	}
	if c.Command == "" {
		return fmt.Errorf("command is required")
	}
	cmd := exec.CommandContext(ctx, c.Command, c.Args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("shell error: %v; out=%s", err, string(out))
	}
	return nil
}
