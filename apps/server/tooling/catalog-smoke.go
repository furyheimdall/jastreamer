//go:build ignore

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

type smokeResult struct {
	TrackCount    int      `json:"track_count"`
	Formats       []string `json:"formats"`
	Generation    uint64   `json:"generation"`
	Revision      uint64   `json:"revision"`
	SQLiteVersion int      `json:"sqlite_version"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	if len(os.Args) != 3 {
		return errors.New("usage: go run ./tooling/catalog-smoke.go <fixture-dir> <migration>")
	}
	mediaRoot, err := os.MkdirTemp("", "jastreamer-catalog-smoke-")
	if err != nil {
		return fmt.Errorf("create smoke directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(mediaRoot)) }()
	for _, name := range []string{"real.flac", "real.mp3", "real.ogg", "real.opus", "real.wav"} {
		encoded, readErr := os.ReadFile(filepath.Join(os.Args[1], name+".b64"))
		if readErr != nil {
			return fmt.Errorf("read %s fixture: %w", name, readErr)
		}
		data, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		if decodeErr != nil {
			return fmt.Errorf("decode %s fixture: %w", name, decodeErr)
		}
		if writeErr := os.WriteFile(filepath.Join(mediaRoot, name), data, 0o600); writeErr != nil {
			return fmt.Errorf("write %s fixture: %w", name, writeErr)
		}
	}
	schema, err := os.ReadFile(os.Args[2])
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	scanner, err := catalog.NewScanner(mediaRoot)
	if err != nil {
		return err
	}
	scan, err := scanner.Scan(context.Background(), catalog.EmptySnapshot())
	if err != nil {
		return err
	}
	store, err := catalog.OpenStore(context.Background(), catalog.StoreConfig{
		Path: filepath.Join(mediaRoot, "catalog.sqlite"), Root: mediaRoot,
		Schema: string(schema), Now: time.Now,
	})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if err := store.Save(context.Background(), scan); err != nil {
		return err
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		return err
	}
	formats := make([]string, 0, len(loaded.Tracks))
	for _, track := range loaded.Tracks {
		if track.Available {
			formats = append(formats, string(track.Format))
		}
	}
	slices.Sort(formats)
	output, err := json.Marshal(smokeResult{
		TrackCount: len(formats), Formats: formats, Generation: loaded.Generation,
		Revision: loaded.Revision, SQLiteVersion: store.SQLiteVersion(),
	})
	if err != nil {
		return fmt.Errorf("encode smoke result: %w", err)
	}
	fmt.Println(string(output))
	return nil
}
