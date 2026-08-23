package decision

import "fmt"

func ParsePolicy(raw string) (Policy, error) {
	policy := Policy(raw)
	if !policy.Valid() {
		return "", fmt.Errorf("policy %q must be stop, album, or similar", raw)
	}
	return policy, nil
}

func (policy Policy) Valid() bool {
	switch policy {
	case PolicyStop, PolicyAlbum, PolicySimilar:
		return true
	default:
		return false
	}
}
