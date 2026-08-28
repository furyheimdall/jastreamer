package transcode

import (
	"fmt"
	"regexp"
	"strconv"
)

const (
	minimumFFmpegMajor = 6
	maximumFFmpegMajor = 8
)

var ffmpegVersionPattern = regexp.MustCompile(`^ffmpeg version ([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+][^ ]+)?(?: |$)`)

type VersionErrorKind string

const (
	VersionMalformed   VersionErrorKind = "malformed"
	VersionUnsupported VersionErrorKind = "unsupported"
)

type VersionError struct {
	Kind    VersionErrorKind
	Version Version
}

func (value *VersionError) Error() string {
	if value.Kind == VersionUnsupported {
		return fmt.Sprintf("transcode: FFmpeg version %s is outside supported major versions %d and %d", value.Version, minimumFFmpegMajor, maximumFFmpegMajor-1)
	}
	return "transcode: malformed FFmpeg version"
}

type Version struct {
	Major int
	Minor int
	Patch int
}

func (value Version) String() string {
	return fmt.Sprintf("%d.%d.%d", value.Major, value.Minor, value.Patch)
}

func parseFFmpegVersion(output string) (Version, error) {
	matches := ffmpegVersionPattern.FindStringSubmatch(firstLine(output))
	if len(matches) != 4 {
		return Version{}, &VersionError{Kind: VersionMalformed}
	}
	parts := [3]int{}
	for index, match := range matches[1:] {
		part, err := strconv.Atoi(match)
		if err != nil {
			return Version{}, &VersionError{Kind: VersionMalformed}
		}
		parts[index] = part
	}
	version := Version{Major: parts[0], Minor: parts[1], Patch: parts[2]}
	if version.Major < minimumFFmpegMajor || version.Major >= maximumFFmpegMajor {
		return Version{}, &VersionError{Kind: VersionUnsupported, Version: version}
	}
	return version, nil
}
