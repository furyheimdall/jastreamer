package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jakestreamer/jstreamer-server/internal/analysis"
	"github.com/jakestreamer/jstreamer-server/internal/catalog"
)

type metric struct {
	Fixture, Quantized                           string
	Tempo, Loudness, Centroid, Contrast, Rolloff float64
	Key                                          int
	Chroma, MFCC                                 [12]float64
}
type targetReport struct {
	Target                                string
	Metrics                               []metric
	RestartAt, Jobs, Executions, Complete int
	NoDuplicateWork                       bool
}

func main() {
	fixture := flag.String("fixture", "", "fixture directory")
	restart := flag.Int("restart-at", 50, "restart boundary")
	target := flag.String("target", "", "target label")
	flag.Parse()
	report, err := run(context.Background(), *fixture, *target, *restart)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(65)
	}
	json.NewEncoder(os.Stdout).Encode(report)
}
func run(ctx context.Context, fixture, target string, restart int) (report targetReport, err error) {
	report = targetReport{Target: target, RestartAt: restart, Jobs: 100, NoDuplicateWork: true}
	fixtureTemp, tempErr := os.MkdirTemp("", "analysis-fixtures-")
	if tempErr != nil {
		return report, tempErr
	}
	defer func() { err = errors.Join(err, os.RemoveAll(fixtureTemp)) }()
	entries, err := filepath.Glob(filepath.Join(fixture, "*.b64"))
	if err != nil || len(entries) != 5 {
		return report, errors.New("expected five golden fixtures")
	}
	sort.Strings(entries)
	var wav []byte
	for _, entry := range entries {
		data, readErr := decodeFixture(entry)
		if readErr != nil {
			return report, readErr
		}
		name := strings.TrimSuffix(filepath.Base(entry), ".b64")
		path := filepath.Join(fixtureTemp, name)
		if writeErr := os.WriteFile(path, data, 0600); writeErr != nil {
			return report, writeErr
		}
		features, analyzeErr := analysis.AnalyzeFile(ctx, path, nil)
		removeErr := os.Remove(path)
		if analyzeErr != nil || removeErr != nil {
			return report, errors.Join(analyzeErr, removeErr)
		}
		report.Metrics = append(report.Metrics, metric{name, hex.EncodeToString(features.Vector), features.Tempo, features.Loudness, features.Centroid, features.Contrast, features.Rolloff, features.Key, features.Chroma, features.MFCC})
		if strings.HasSuffix(name, ".wav") {
			wav = data
		}
	}
	if restart < 0 || restart > report.Jobs || len(wav) == 0 {
		return report, errors.New("invalid restart boundary")
	}
	temp, err := os.MkdirTemp("", "analysis-durable-parity-")
	if err != nil {
		return report, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(temp)) }()
	root := filepath.Join(temp, "music")
	if err = os.Mkdir(root, 0700); err != nil {
		return report, err
	}
	for i := 0; i < report.Jobs; i++ {
		if err = os.WriteFile(filepath.Join(root, fmt.Sprintf("%03d.wav", i)), wav, 0600); err != nil {
			return report, err
		}
	}
	schema, err := os.ReadFile("migrations/001_catalog.sql")
	if err != nil {
		return report, err
	}
	config := catalog.StoreConfig{Path: filepath.Join(temp, "catalog.db"), Root: root, Schema: string(schema), Now: time.Now}
	store, err := catalog.OpenStore(ctx, config)
	if err != nil {
		return report, err
	}
	scanner, err := catalog.NewScanner(root)
	if err != nil {
		return report, errors.Join(err, store.Close())
	}
	scan, err := scanner.Scan(ctx, catalog.EmptySnapshot())
	if err == nil {
		err = store.Save(ctx, scan)
	}
	if err != nil {
		return report, errors.Join(err, store.Close())
	}
	first := catalog.NewProcessor(store, 4)
	count, err := first.Process(restart)
	first.Close()
	err = errors.Join(err, store.Close())
	if err != nil {
		return report, err
	}
	report.Executions = count
	store, err = catalog.OpenStore(ctx, config)
	if err != nil {
		return report, err
	}
	second := catalog.NewProcessor(store, 4)
	count, err = second.Process(0)
	second.Close()
	report.Executions += count
	if err == nil {
		var jobs []catalog.AnalysisJob
		jobs, err = store.ScheduleAnalysis(ctx, analysis.CurrentProvenance())
		report.NoDuplicateWork = len(jobs) == 0 && report.Executions == report.Jobs
	}
	if err == nil {
		var snapshot catalog.Snapshot
		snapshot, err = store.Load(ctx)
		if err == nil {
			for _, track := range snapshot.Tracks {
				if track.AnalysisStatus == catalog.AnalysisComplete {
					report.Complete++
				}
			}
		}
	}
	err = errors.Join(err, store.Close())
	return report, err
}
func decodeFixture(path string) ([]byte, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
}
