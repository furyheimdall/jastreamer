package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func newEventEpoch() (uint64, error) {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return 0, fmt.Errorf("generate event epoch: %w", err)
	}
	epoch := binary.BigEndian.Uint64(bytes[:])
	if epoch == 0 {
		epoch = 1
	}
	return epoch, nil
}

func startInitialScans(ctx context.Context, coordinator *catalog.Coordinator) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, root := range coordinator.Roots() {
			job, err := coordinator.StartScan(ctx, root.ID)
			if err != nil {
				return
			}
			if _, err := coordinator.Wait(ctx, job.ID); err != nil {
				return
			}
		}
	}()
	return done
}
