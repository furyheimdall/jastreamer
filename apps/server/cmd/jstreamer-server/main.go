package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config, err := loadConfig(os.Args[1:])
	if err == nil {
		err = run(ctx, config)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "jstreamer-server: %v\n", err)
		os.Exit(1)
	}
}
