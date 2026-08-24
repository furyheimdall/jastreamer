package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
type report struct {
	SchemaVersion        int `json:"schema_version"`
	Analyzer, Normalizer string
	Tolerances           map[string]float64
	Platforms            []targetReport
	Canonical            []metric
}
type golden struct {
	Tolerances map[string]float64 `json:"tolerances"`
	Vectors    []metric           `json:"vectors"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(65)
	}
}
func run() error {
	platforms := flag.String("platform", "", "comma-separated GOOS/GOARCH targets")
	fixture := flag.String("fixture", "", "fixture directory")
	restart := flag.Int("restart-at", 50, "restart boundary")
	output := flag.String("output", "", "JSON output")
	flag.Parse()
	if *platforms == "" || *fixture == "" || *output == "" {
		return errors.New("platform, fixture, and output are required")
	}
	root := os.Getenv("JASTREAMER_ROOT")
	resolve := func(path string) string {
		if filepath.IsAbs(path) || root == "" {
			return path
		}
		return filepath.Join(root, path)
	}
	tolerances := map[string]float64{"tempo_bpm": .25, "loudness_db": .05, "frequency_hz": 1, "contrast_db": .25, "normalized_band": .005}
	result := report{SchemaVersion: 1, Analyzer: "jastreamer-classical@1.0.0", Normalizer: "acoustic-linear@1.0.0", Tolerances: tolerances}
	for _, target := range strings.Split(*platforms, ",") {
		platform, err := runTarget(target, resolve(*fixture), *restart)
		if err != nil {
			return err
		}
		if platform.Executions != 100 || platform.Complete != 100 || !platform.NoDuplicateWork {
			return fmt.Errorf("durable restart failed on %s: %+v", target, platform)
		}
		result.Platforms = append(result.Platforms, platform)
	}
	if len(result.Platforms) == 0 {
		return errors.New("no platforms")
	}
	result.Canonical = result.Platforms[0].Metrics
	goldenData, err := os.ReadFile(filepath.Join(resolve(*fixture), "golden.json"))
	if err != nil {
		return err
	}
	var expected golden
	if err = json.Unmarshal(goldenData, &expected); err != nil {
		return err
	}
	if err = compareGolden(result.Canonical, expected); err != nil {
		return err
	}
	for _, platform := range result.Platforms[1:] {
		if err = compareMetrics(result.Canonical, platform.Metrics, tolerances); err != nil {
			return fmt.Errorf("%s: %w", platform.Target, err)
		}
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	path := resolve(*output)
	if err = os.MkdirAll(filepath.Dir(path), 0755); err == nil {
		err = os.WriteFile(path, append(data, '\n'), 0644)
	}
	if err == nil {
		fmt.Println(string(data))
	}
	return err
}
func runTarget(target, fixture string, restart int) (targetReport, error) {
	parts := strings.Split(target, "/")
	if len(parts) != 2 || parts[0] != "linux" {
		return targetReport{}, fmt.Errorf("unsupported platform %s", target)
	}
	temp, err := os.MkdirTemp("", "analysis-parity-target-")
	if err != nil {
		return targetReport{}, err
	}
	defer os.RemoveAll(temp)
	binary := filepath.Join(temp, "worker")
	build := exec.Command("go", "build", "-o", binary, "./tooling/analysisparityworker")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+parts[0], "GOARCH="+parts[1])
	if data, buildErr := build.CombinedOutput(); buildErr != nil {
		return targetReport{}, fmt.Errorf("cross-build %s: %w: %s", target, buildErr, data)
	}
	command := []string{binary, "--fixture", fixture, "--target", target, "--restart-at", fmt.Sprint(restart)}
	if parts[1] != runtime.GOARCH {
		emulator, lookupErr := exec.LookPath("qemu-" + parts[1])
		if lookupErr != nil {
			return targetReport{}, lookupErr
		}
		command = append([]string{emulator}, command...)
	}
	data, runErr := exec.Command(command[0], command[1:]...).CombinedOutput()
	if runErr != nil {
		return targetReport{}, fmt.Errorf("execute %s: %w: %s", target, runErr, data)
	}
	var result targetReport
	if err = json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	return result, nil
}
func compareGolden(got []metric, want golden) error {
	if len(got) != len(want.Vectors) {
		return errors.New("golden fixture count")
	}
	for i, expected := range want.Vectors {
		actual := got[i]
		if actual.Fixture != expected.Fixture || actual.Quantized != expected.Quantized || actual.Key != expected.Key || math.Abs(actual.Tempo-expected.Tempo) > want.Tolerances["tempo_bpm"] || math.Abs(actual.Loudness-expected.Loudness) > want.Tolerances["loudness_db"] || math.Abs(actual.Centroid-expected.Centroid) > want.Tolerances["frequency_hz"] || math.Abs(actual.Rolloff-expected.Rolloff) > want.Tolerances["frequency_hz"] {
			return fmt.Errorf("golden mismatch %s", actual.Fixture)
		}
	}
	return nil
}
func compareMetrics(left, right []metric, t map[string]float64) error {
	if len(left) != len(right) {
		return errors.New("platform fixture count")
	}
	for i, a := range left {
		b := right[i]
		if a.Fixture != b.Fixture || a.Quantized != b.Quantized || a.Key != b.Key || math.Abs(a.Tempo-b.Tempo) > t["tempo_bpm"] || math.Abs(a.Loudness-b.Loudness) > t["loudness_db"] || math.Abs(a.Centroid-b.Centroid) > t["frequency_hz"] || math.Abs(a.Contrast-b.Contrast) > t["contrast_db"] || math.Abs(a.Rolloff-b.Rolloff) > t["frequency_hz"] {
			return fmt.Errorf("scalar mismatch %s", a.Fixture)
		}
		for j := 0; j < 12; j++ {
			if math.Abs(a.Chroma[j]-b.Chroma[j]) > t["normalized_band"] || math.Abs(a.MFCC[j]-b.MFCC[j]) > t["normalized_band"] {
				return fmt.Errorf("band mismatch %s[%d]", a.Fixture, j)
			}
		}
	}
	return nil
}
