package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const MaximumRootCount = 32

var (
	ErrDuplicateRoot          = errors.New("catalog: duplicate root")
	ErrTooManyRoots           = errors.New("catalog: too many roots")
	ErrRootOutsideAllowedBase = errors.New("catalog: root outside allowed base")
	ErrUnreadableRoot         = errors.New("catalog: unreadable root")
	ErrRootNotFound           = errors.New("catalog: root not found")
)

type RootID string

type Root struct {
	ID            RootID `json:"root_id"`
	DisplayName   string `json:"display_name"`
	CanonicalPath string `json:"-"`
}

type RootRegistry struct {
	mu           sync.RWMutex
	allowedBases []string
	roots        map[RootID]Root
}

func NewRootRegistry(allowedBases []string) (*RootRegistry, error) {
	if len(allowedBases) == 0 {
		return nil, fmt.Errorf("allowed bases: %w", ErrRootOutsideAllowedBase)
	}
	canonical := make([]string, 0, len(allowedBases))
	for _, base := range allowedBases {
		path, err := canonicalRoot(base)
		if err != nil {
			return nil, fmt.Errorf("allowed base: %w", err)
		}
		canonical = append(canonical, path)
	}
	return &RootRegistry{allowedBases: canonical, roots: make(map[RootID]Root)}, nil
}

func (registry *RootRegistry) Add(path, displayName string) (Root, error) {
	root, err := registry.prepare(path, displayName)
	if err != nil {
		return Root{}, err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, existing := range registry.roots {
		if existing.CanonicalPath == root.CanonicalPath {
			return Root{}, ErrDuplicateRoot
		}
	}
	if len(registry.roots) >= MaximumRootCount {
		return Root{}, ErrTooManyRoots
	}
	registry.roots[root.ID] = root
	return root, nil
}

func (registry *RootRegistry) prepare(path, displayName string) (Root, error) {
	canonical, err := canonicalRoot(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return Root{}, ErrUnreadableRoot
		}
		return Root{}, err
	}
	if !registry.allowed(canonical) {
		return Root{}, ErrRootOutsideAllowedBase
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o500 != 0o500 {
		return Root{}, ErrUnreadableRoot
	}
	directory, err := os.Open(canonical)
	if err != nil {
		return Root{}, ErrUnreadableRoot
	}
	if closeErr := directory.Close(); closeErr != nil {
		return Root{}, fmt.Errorf("close root: %w", closeErr)
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = filepath.Base(canonical)
	}
	return Root{ID: RootID(hashID("logical-root", canonical)), DisplayName: name, CanonicalPath: canonical}, nil
}

func (registry *RootRegistry) reconciled(desired []DesiredRoot) (*RootRegistry, error) {
	next, err := NewRootRegistry(registry.allowedBases)
	if err != nil {
		return nil, err
	}
	for _, definition := range desired {
		if definition.ID == "" {
			return nil, ErrRootNotFound
		}
		root, err := next.prepare(definition.Path, definition.DisplayName)
		if err != nil {
			return nil, err
		}
		root.ID = definition.ID
		if _, exists := next.roots[root.ID]; exists {
			return nil, ErrDuplicateRoot
		}
		for _, existing := range next.roots {
			if existing.CanonicalPath == root.CanonicalPath {
				return nil, ErrDuplicateRoot
			}
		}
		next.roots[root.ID] = root
	}
	if len(next.roots) > MaximumRootCount {
		return nil, ErrTooManyRoots
	}
	return next, nil
}

func (registry *RootRegistry) allowed(path string) bool {
	for _, base := range registry.allowedBases {
		if isWithin(base, path) {
			return true
		}
	}
	return false
}

func (registry *RootRegistry) Get(id RootID) (Root, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	root, found := registry.roots[id]
	if !found {
		return Root{}, ErrRootNotFound
	}
	return root, nil
}

func (registry *RootRegistry) Roots() []Root {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	roots := make([]Root, 0, len(registry.roots))
	for _, root := range registry.roots {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(left, right int) bool { return roots[left].ID < roots[right].ID })
	return roots
}

func (registry *RootRegistry) remove(id RootID) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.roots, id)
}

func (registry *RootRegistry) restore(root Root) error {
	prepared, err := registry.prepare(root.CanonicalPath, root.DisplayName)
	if err != nil {
		return err
	}
	if root.ID == "" {
		return errors.New("catalog: persisted root identity is empty")
	}
	prepared.ID = root.ID
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.roots[prepared.ID]; exists {
		return ErrDuplicateRoot
	}
	for _, existing := range registry.roots {
		if existing.CanonicalPath == prepared.CanonicalPath {
			return ErrDuplicateRoot
		}
	}
	if len(registry.roots) >= MaximumRootCount {
		return ErrTooManyRoots
	}
	registry.roots[prepared.ID] = prepared
	return nil
}
