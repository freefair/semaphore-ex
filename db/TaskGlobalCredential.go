package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	MaxTaskGlobalCredentialBindings  = 64
	maxTaskGlobalCredentialJSONBytes = 16 * 1024
)

func (task *Task) EncodeGlobalCredentialBindings() error {
	if task.GlobalCredentialBindings == nil {
		if task.GlobalCredentialBindingsJSON == "" {
			task.GlobalCredentialBindingsJSON = "{}"
			return nil
		}
		if err := task.DecodeGlobalCredentialBindings(); err != nil {
			return err
		}
	}
	if err := validateTaskGlobalCredentialBindings(task.GlobalCredentialBindings); err != nil {
		return err
	}
	encoded, err := json.Marshal(task.GlobalCredentialBindings)
	if err != nil {
		return fmt.Errorf("encode task global credential bindings: %w", err)
	}
	if len(encoded) > maxTaskGlobalCredentialJSONBytes {
		return errors.New("task global credential bindings are too large")
	}
	task.GlobalCredentialBindingsJSON = string(encoded)
	return nil
}

func (task *Task) DecodeGlobalCredentialBindings() error {
	raw := []byte(task.GlobalCredentialBindingsJSON)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if len(raw) > maxTaskGlobalCredentialJSONBytes || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("task global credential bindings are invalid")
	}
	bindings := make(map[string]int)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&bindings); err != nil {
		return errors.New("task global credential bindings are invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("task global credential bindings are invalid")
	}
	if err := validateTaskGlobalCredentialBindings(bindings); err != nil {
		return err
	}
	task.GlobalCredentialBindings = bindings
	task.GlobalCredentialBindingsJSON = string(raw)
	return nil
}

func validateTaskGlobalCredentialBindings(bindings map[string]int) error {
	if len(bindings) > MaxTaskGlobalCredentialBindings {
		return fmt.Errorf("task global credential bindings exceed maximum count %d", MaxTaskGlobalCredentialBindings)
	}
	for name, credentialID := range bindings {
		if !workflowArtifactNamePattern.MatchString(name) || credentialID <= 0 {
			return errors.New("task global credential binding is invalid")
		}
	}
	return nil
}
