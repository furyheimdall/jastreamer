package playback

import (
	"fmt"
	"os"
)

func migrationStatements(config Config, version int) ([]string, error) {
	var paths []string
	switch version {
	case 0:
		paths = []string{config.MigrationPath, config.ExpansionPath}
	case 2:
		paths = []string{config.ExpansionPath}
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
