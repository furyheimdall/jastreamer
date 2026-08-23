//go:build ignore

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const wineImage = "scottyhardy/docker-wine:stable-10.0"

type targetResult struct {
	Platform string          `json:"platform"`
	SHA256   string          `json:"sha256"`
	Decision json.RawMessage `json:"decision"`
}

type benchmarkResult struct {
	Tracks             int    `json:"tracks"`
	Samples            int    `json:"samples"`
	P95Nanoseconds     int64  `json:"p95_nanoseconds"`
	StorageBytes       uint64 `json:"storage_bytes"`
	ResidentBytes      uint64 `json:"resident_bytes"`
	MaximumNanoseconds int64  `json:"maximum_nanoseconds"`
}

type report struct {
	FixtureSHA256 string          `json:"fixture_sha256"`
	ByteIdentical bool            `json:"byte_identical"`
	Targets       []targetResult  `json:"targets"`
	Benchmark     benchmarkResult `json:"benchmark"`
}

func main() {
	platforms := flag.String("platform", "", "comma-separated GOOS/GOARCH targets")
	fixture := flag.String("fixture", "", "fixture path")
	output := flag.String("output", "", "report path")
	flag.Parse()
	if *platforms == "" || *fixture == "" || *output == "" {
		exit(errors.New("--platform, --fixture, and --output are required"))
	}
	root := os.Getenv("JSTREAMER_ROOT")
	fixtureValue := *fixture
	if !filepath.IsAbs(fixtureValue) {
		if root != "" {
			fixtureValue = filepath.Join(root, fixtureValue)
		}
	}
	outputPath := *output
	if !filepath.IsAbs(outputPath) && root != "" {
		outputPath = filepath.Join(root, outputPath)
	}
	fixturePath, err := filepath.Abs(fixtureValue)
	if err != nil {
		exit(err)
	}
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		exit(err)
	}
	temp, err := os.MkdirTemp("", "jstreamer-ranking-parity-*")
	if err != nil {
		exit(err)
	}
	defer os.RemoveAll(temp)

	platformList := strings.Split(*platforms, ",")
	results := make([]targetResult, 0, len(platformList))
	var reference []byte
	for _, platform := range platformList {
		platform = strings.TrimSpace(platform)
		decision, runErr := runTarget(temp, fixturePath, platform)
		if runErr != nil {
			exit(fmt.Errorf("%s: %w", platform, runErr))
		}
		decision = bytes.TrimSpace(decision)
		if !json.Valid(decision) {
			exit(fmt.Errorf("%s emitted invalid JSON %q", platform, decision))
		}
		if reference == nil {
			reference = bytes.Clone(decision)
		} else if !bytes.Equal(reference, decision) {
			exit(fmt.Errorf("%s decision differs from reference", platform))
		}
		sum := sha256.Sum256(decision)
		results = append(results, targetResult{
			Platform: platform, SHA256: hex.EncodeToString(sum[:]), Decision: decision,
		})
	}
	benchmarkBytes, err := runNative(temp, fixturePath, "linux/"+runtime.GOARCH, true)
	if err != nil {
		exit(err)
	}
	var benchmark benchmarkResult
	if err := json.Unmarshal(benchmarkBytes, &benchmark); err != nil {
		exit(fmt.Errorf("decode benchmark: %w", err))
	}
	if benchmark.P95Nanoseconds >= int64(250*time.Millisecond) {
		exit(fmt.Errorf("100000-track warm p95 %s exceeds 250ms", time.Duration(benchmark.P95Nanoseconds)))
	}
	fixtureSum := sha256.Sum256(fixtureBytes)
	value := report{
		FixtureSHA256: hex.EncodeToString(fixtureSum[:]), ByteIdentical: true,
		Targets: results, Benchmark: benchmark,
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		exit(err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		exit(err)
	}
	if err := os.WriteFile(outputPath, append(payload, '\n'), 0o644); err != nil {
		exit(err)
	}
	fmt.Printf("ranking parity passed: %d targets, p95=%s, report=%s\n",
		len(results), time.Duration(benchmark.P95Nanoseconds), outputPath)
}

func runTarget(temp, fixture, platform string) ([]byte, error) {
	parts := strings.Split(platform, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid platform %q", platform)
	}
	goos, goarch := parts[0], parts[1]
	extension := ""
	if goos == "windows" {
		extension = ".exe"
	}
	binary := filepath.Join(temp, "ranking-"+goos+"-"+goarch+extension)
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./tooling/rankingparityworker")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build: %w: %s", err, output)
	}
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		return commandOutput(exec.Command(binary, "--fixture", fixture))
	}
	switch goos {
	case "linux":
		return runLinuxContainer(temp, fixture, binary, goarch)
	case "windows":
		if goarch != "amd64" {
			return nil, fmt.Errorf("unsupported Windows architecture %s", goarch)
		}
		return runWineContainer(temp, fixture, binary)
	default:
		return nil, fmt.Errorf("unsupported target %s", platform)
	}
}

func runLinuxContainer(temp, fixture, binary, arch string) ([]byte, error) {
	fixtureDir, fixtureName := filepath.Dir(fixture), filepath.Base(fixture)
	args := []string{
		"run", "--rm", "--network", "none", "--platform", "linux/" + arch,
		"--volume", temp + ":/work:ro", "--volume", fixtureDir + ":/fixture:ro",
		"alpine:3.22", "/work/" + filepath.Base(binary), "--fixture", "/fixture/" + fixtureName,
	}
	return commandOutput(exec.Command("docker", args...))
}

func runWineContainer(temp, fixture, binary string) ([]byte, error) {
	fixtureDir, fixtureName := filepath.Dir(fixture), filepath.Base(fixture)
	prefix := filepath.Join(temp, "wine-prefix")
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		return nil, fmt.Errorf("create Wine prefix: %w", err)
	}
	args := []string{
		"run", "--rm", "--network", "none", "--platform", "linux/amd64",
		"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"--env", "WINEDEBUG=-all", "--env", `WINEDLLOVERRIDES=mscoree,mshtml=`,
		"--env", "WINEPREFIX=/tmp/wine", "--env", "XDG_RUNTIME_DIR=/tmp",
		"--volume", prefix + ":/tmp/wine",
		"--volume", temp + ":/work:ro",
		"--volume", fixtureDir + ":/fixture:ro", "--entrypoint", "wine64",
		wineImage, `Z:\work\` + filepath.Base(binary),
		"--fixture", `Z:\fixture\` + fixtureName,
	}
	return commandOutput(exec.Command("docker", args...))
}

func runNative(temp, fixture, platform string, benchmark bool) ([]byte, error) {
	parts := strings.Split(platform, "/")
	binary := filepath.Join(temp, "ranking-benchmark")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./tooling/rankingparityworker")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+parts[0], "GOARCH="+parts[1])
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build benchmark: %w: %s", err, output)
	}
	args := []string{"--fixture", fixture}
	if benchmark {
		args = []string{"--benchmark"}
	}
	return commandOutput(exec.Command(binary, args...))
}

func commandOutput(command *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
