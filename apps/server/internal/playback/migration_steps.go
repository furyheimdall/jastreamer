package playback

import (
	"fmt"
	"os"
	"path/filepath"
)

func migrationStatements(config Config, version int) ([]string, error) {
	directory := filepath.Dir(config.ExpansionPath)
	serverStatePath := filepath.Join(directory, "004_server_state.sql")
	rendererSessionPath := filepath.Join(directory, "005_renderer_sessions.sql")
	transportMutationPath := filepath.Join(directory, "006_transport_mutations.sql")
	previousHistoryPath := filepath.Join(directory, "007_previous_history.sql")
	var paths []string
	switch version {
	case 0:
		paths = []string{config.MigrationPath, config.ExpansionPath, serverStatePath, rendererSessionPath, transportMutationPath, previousHistoryPath}
	case 2:
		paths = []string{config.ExpansionPath, serverStatePath, rendererSessionPath, transportMutationPath, previousHistoryPath}
	case 3:
		paths = []string{serverStatePath, rendererSessionPath, transportMutationPath, previousHistoryPath}
	case 4:
		paths = []string{rendererSessionPath, transportMutationPath, previousHistoryPath}
	case 5:
		paths = []string{transportMutationPath, previousHistoryPath}
	case 6:
		paths = []string{previousHistoryPath}
	default:
		return nil, fmt.Errorf("unsupported migration from schema %d", version)
	}
	statements := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			return nil, fmt.Errorf("migration path for schema %d is empty", version)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read playback migration %s: %w", path, err)
		}
		statements = append(statements, string(content))
	}
	return statements, nil
}
