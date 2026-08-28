package playback

import "time"

type Clock interface{ Now() time.Time }

type Config struct {
	Path            string
	MigrationPath   string
	ExpansionPath   string
	BackupDirectory string
	SupportedSchema int
	JournalMode     JournalMode
	Clock           Clock
}
