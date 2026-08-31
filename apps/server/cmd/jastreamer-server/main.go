package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const serverUsage = "Usage: jastreamer-server [--config <path>] [--tls-certificate <path> --tls-private-key <path>]"

func main() {
	os.Exit(mainExitCode(os.Args[1:], os.Stdout, os.Stderr))
}

func mainExitCode(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, _ = fmt.Fprintln(stdout, serverUsage)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config, err := loadConfig(args)
	if err == nil {
		err = run(ctx, config)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "jastreamer-server: %v\n", err)
		return 1
	}
	return 0
}
