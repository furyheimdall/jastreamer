package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

func normalizeLocks(locks Locks) (Locks, error) {
	if locks.DataDirectory == "" || !filepath.IsAbs(locks.DataDirectory) {
		return Locks{}, fmt.Errorf("settings data directory lock must be absolute")
	}
	bases := make([]string, len(locks.AllowedCatalogBases))
	for index, base := range locks.AllowedCatalogBases {
		resolved, err := filepath.EvalSymlinks(base)
		if err != nil {
			return Locks{}, fmt.Errorf("resolve allowed catalog base %d: %w", index, err)
		}
		bases[index], err = filepath.Abs(resolved)
		if err != nil {
			return Locks{}, fmt.Errorf("absolute allowed catalog base %d: %w", index, err)
		}
		info, err := os.Stat(bases[index])
		if err != nil || !info.IsDir() {
			return Locks{}, fmt.Errorf("allowed catalog base %d must be an existing directory", index)
		}
	}
	locks.AllowedCatalogBases = bases
	locks.CertificateSANs = slices.Clone(locks.CertificateSANs)
	locks.EnvironmentLockedFields = slices.Clone(locks.EnvironmentLockedFields)
	sort.Strings(locks.EnvironmentLockedFields)
	return locks, nil
}

func applyUpdate(current Values, update Update) Values {
	next := cloneValues(current)
	if update.DisplayName != nil {
		next.DisplayName = *update.DisplayName
	}
	if update.CatalogRoots != nil {
		next.CatalogRoots = slices.Clone(*update.CatalogRoots)
	}
	if update.ControlOrigins != nil {
		next.ControlOrigins = slices.Clone(*update.ControlOrigins)
	}
	if update.PairingTTLSeconds != nil {
		next.PairingTTLSeconds = *update.PairingTTLSeconds
	}
	if update.UPnPInterfaces != nil {
		next.UPnPInterfaces = slices.Clone(*update.UPnPInterfaces)
	}
	if update.K17HTTP != nil {
		next.K17HTTP = *update.K17HTTP
	}
	if update.FFmpegPath != nil {
		next.FFmpegPath = *update.FFmpegPath
	}
	return next
}

func cloneValues(value Values) Values {
	value.CatalogRoots = slices.Clone(value.CatalogRoots)
	value.ControlOrigins = slices.Clone(value.ControlOrigins)
	value.UPnPInterfaces = slices.Clone(value.UPnPInterfaces)
	return value
}

func cloneLocks(value Locks) Locks {
	value.CertificateSANs = slices.Clone(value.CertificateSANs)
	value.AllowedCatalogBases = slices.Clone(value.AllowedCatalogBases)
	value.EnvironmentLockedFields = slices.Clone(value.EnvironmentLockedFields)
	return value
}
