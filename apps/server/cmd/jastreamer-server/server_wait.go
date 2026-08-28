package main

import (
	"context"
	"errors"
	"time"
)

func waitForServer(ctx context.Context, serverErrors, processorErrors <-chan error, shutdown func(context.Context) error) error {
	select {
	case err := <-serverErrors:
		return err
	case processorErr := <-processorErrors:
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return errors.Join(processorErr, shutdown(shutdownContext))
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return shutdown(shutdownContext)
	}
}
