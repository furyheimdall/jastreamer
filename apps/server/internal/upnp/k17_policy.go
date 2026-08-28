package upnp

import (
	"strconv"
	"strings"
)

const maxFirmwareDigits = 6

func firmwareAtLeast(actual, minimum string) bool {
	actualValue, actualOK := parseFirmware(actual)
	minimumValue, minimumOK := parseFirmware(minimum)
	return actualOK && minimumOK && actualValue >= minimumValue
}

func parseFirmware(raw string) (uint64, bool) {
	if len(raw) < 2 || len(raw) > maxFirmwareDigits+1 || raw[0] != 'V' {
		return 0, false
	}
	for _, value := range raw[1:] {
		if value < '0' || value > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(raw[1:], 10, 32)
	return parsed, err == nil
}

func compatibleProtocols(sink string, accepted []string) []string {
	result := []string{}
	seen := map[string]struct{}{}
	for value := range strings.SplitSeq(sink, ",") {
		candidate, ok := parseProtocol(value)
		if !ok {
			continue
		}
		for _, rawAccepted := range accepted {
			allowed, valid := parseProtocol(rawAccepted)
			if valid && protocolCompatible(candidate, allowed) {
				key := strings.ToLower(candidate.mime)
				if _, found := seen[key]; !found {
					seen[key] = struct{}{}
					result = append(result, candidate.mime)
				}
				break
			}
		}
	}
	return result
}

type protocol struct {
	transfer string
	network  string
	mime     string
}

func parseProtocol(raw string) (protocol, bool) {
	fields := strings.Split(strings.TrimSpace(raw), ":")
	if len(fields) != 4 {
		return protocol{}, false
	}
	transfer := strings.ToLower(strings.TrimSpace(fields[0]))
	network := strings.TrimSpace(fields[1])
	mime := strings.TrimSpace(strings.SplitN(fields[2], ";", 2)[0])
	if (transfer != "http-get" && transfer != "*") || network == "" || mime == "" || !strings.Contains(mime, "/") {
		return protocol{}, false
	}
	return protocol{transfer: transfer, network: network, mime: mime}, true
}

func protocolCompatible(sink, accepted protocol) bool {
	return fieldMatches(sink.transfer, accepted.transfer) && fieldMatches(sink.network, accepted.network) && mediaMatches(sink.mime, accepted.mime)
}

func fieldMatches(left, right string) bool {
	return left == "*" || right == "*" || strings.EqualFold(left, right)
}

func mediaMatches(left, right string) bool {
	if left == "*" || right == "*" || left == "*/*" || right == "*/*" {
		return true
	}
	leftType, leftSubtype, leftOK := strings.Cut(left, "/")
	rightType, rightSubtype, rightOK := strings.Cut(right, "/")
	if !leftOK || !rightOK || !strings.EqualFold(leftType, rightType) {
		return false
	}
	return leftSubtype == "*" || rightSubtype == "*" || strings.EqualFold(leftSubtype, rightSubtype)
}
