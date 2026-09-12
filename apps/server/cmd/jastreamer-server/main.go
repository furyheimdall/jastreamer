package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jastreamer/jastreamer-server/internal/auth"
	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/database"
	"golang.org/x/term"
)

const serverUsage = "Usage: jastreamer-server [--config PATH] [--version | --init-config PATH | --check-config PATH | --reset-password USER]"

func main() { os.Exit(mainExitCode(os.Args[1:], os.Stdout, os.Stderr)) }

func mainExitCode(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, args, os.Stdin, stdout, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "jastreamer-server: %v\n", err)
		return 1
	}
	return 0
}

func execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	defaultPath := os.Getenv("JASTREAMER_CONFIG")
	if defaultPath == "" {
		defaultPath = "/etc/jastreamer/server.json"
	}
	flags := flag.NewFlagSet("jastreamer-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = fmt.Fprintln(stdout, serverUsage); flags.PrintDefaults() }
	configPath := flags.String("config", defaultPath, "Server JSON configuration path")
	initialize := flags.String("init-config", "", "Write default configuration without replacing a file")
	check := flags.String("check-config", "", "Validate a configuration without starting the server")
	reset := flags.String("reset-password", "", "Reset an account password from terminal or stdin; revoke its sessions")
	version := flags.Bool("version", false, "Print product version and source revision")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments; %s", serverUsage)
	}
	modes := 0
	for _, value := range []string{*initialize, *check, *reset} {
		if value != "" {
			modes++
		}
	}
	if *version {
		modes++
	}
	if modes > 1 {
		return errors.New("choose only one maintenance command")
	}
	if *version {
		_, err := fmt.Fprintf(stdout, "jastreamer-server %s (%s)\n", productVersion, resolvedSourceRevision())
		return err
	}
	if *initialize != "" {
		if err := initializeConfig(*initialize); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "Configuration created. Edit local music roots and listener settings before starting.")
		return err
	}
	if *check != "" {
		if _, err := config.Load(*check); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "Configuration valid.")
		return err
	}
	value, err := config.Load(*configPath)
	if errors.Is(err, os.ErrNotExist) && *reset == "" {
		if err = initializeConfig(*configPath); err == nil {
			value, err = config.Load(*configPath)
		}
	}
	if err != nil {
		return err
	}
	if *reset != "" {
		path := filepath.Join(value.DataDir, "server.sqlite")
		if _, err = os.Stat(path); err != nil {
			return fmt.Errorf("existing account database required: %w", err)
		}
		db, err := database.Open(path)
		if err != nil {
			return err
		}
		defer db.Close()
		accounts, err := auth.New(db)
		if err != nil {
			return err
		}
		password, err := readPassword(stdin, stderr)
		if err != nil {
			return err
		}
		if err = accounts.ResetPassword(ctx, *reset, password); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, "Password reset. Existing account sessions have been revoked.")
		return err
	}
	return runServer(ctx, value, *configPath)
}

func initializeConfig(path string) (err error) {
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(config.Default()); err != nil {
		return err
	}
	return file.Sync()
}

func readPassword(input io.Reader, prompt io.Writer) (string, error) {
	if terminal, ok := input.(*os.File); ok && term.IsTerminal(int(terminal.Fd())) {
		_, _ = fmt.Fprint(prompt, "New password: ")
		value, err := term.ReadPassword(int(terminal.Fd()))
		_, _ = fmt.Fprintln(prompt)
		if err != nil {
			return "", err
		}
		return string(value), nil
	}
	reader := bufio.NewReader(io.LimitReader(input, 1025))
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(value) > 1024 {
		return "", errors.New("password is too long")
	}
	return strings.TrimRight(value, "\r\n"), nil
}
