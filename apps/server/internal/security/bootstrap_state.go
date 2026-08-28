package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func BootstrapComplete(path string) (bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open security state: %w", err)
	}
	defer file.Close()
	var state struct {
		BootstrapComplete bool `json:"bootstrap_complete"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	if err := decoder.Decode(&state); err != nil {
		return false, fmt.Errorf("decode security state: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("decode security state: exactly one object is required")
	}
	return state.BootstrapComplete, nil
}
