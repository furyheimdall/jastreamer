package main

import "runtime/debug"

var (
	productVersion = "0.1.0"
	sourceRevision = ""
)

func resolvedSourceRevision() string {
	if sourceRevision != "" {
		return sourceRevision
	}
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "unknown"
}
