package cast

import (
	"errors"
	"fmt"
	"time"
)

const (
	defaultDiscoveryInterval = 30 * time.Second
	discoveryWindow          = 2 * time.Second
	actionTimeout            = 10 * time.Second
)

var (
	ErrInvalidConfig   = errors.New("invalid Google Cast configuration")
	ErrInvalidResponse = errors.New("invalid Google Cast response")
	ErrInvalidResource = errors.New("invalid Google Cast media resource")
	ErrUnavailable     = errors.New("Google Cast receiver is unavailable")
)

// Config controls Cast receiver discovery. Notify uses the same topic contract
// as the other output backends; device changes publish the "renderers" topic.
type Config struct {
	Interfaces        []string
	DiscoveryInterval time.Duration
	Notify            func(string)
}

func validateConfig(config Config) (Config, error) {
	if config.DiscoveryInterval < 0 {
		return Config{}, fmt.Errorf("cast: discovery interval: %w", ErrInvalidConfig)
	}
	if config.DiscoveryInterval == 0 {
		config.DiscoveryInterval = defaultDiscoveryInterval
	}
	if config.Notify == nil {
		config.Notify = func(string) {}
	}
	return config, nil
}
