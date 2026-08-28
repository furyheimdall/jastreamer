package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

type liveServer struct {
	url    string
	client *http.Client
	stop   func() error
}

func startLiveServer(t *testing.T, directory string) liveServer {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("capture readiness: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = stdout }()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, serverConfig{
			address: "127.0.0.1:0", dataDirectory: directory, catalogRoot: filepath.Join(directory, "media"),
			catalogMigrationPath: "../../migrations/001_catalog.sql", playbackMigrationPath: "../../migrations/002_playback.sql",
			playbackExpansionPath: "../../migrations/003_todo12.sql", setupSecret: "integration-setup",
			certificateDNS: []string{"localhost"}, certificateIPs: []net.IP{net.ParseIP("127.0.0.1")}, pairingTTL: 5 * time.Minute,
		})
	}()
	lineReady := make(chan string, 1)
	readError := make(chan error, 1)
	go func() {
		line, readErr := bufio.NewReader(reader).ReadString('\n')
		if readErr != nil {
			readError <- readErr
			return
		}
		lineReady <- line
	}()
	var line string
	select {
	case line = <-lineReady:
	case readErr := <-readError:
		cancel()
		t.Fatalf("read readiness: %v", readErr)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("server readiness timed out")
	}
	os.Stdout = stdout
	_ = writer.Close()
	_ = reader.Close()
	fields := strings.Fields(line)
	if len(fields) != 3 || fields[0] != "ready" || !strings.HasPrefix(fields[1], "https://") {
		cancel()
		t.Fatalf("readiness line = %q", line)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}}
	var stopOnce sync.Once
	var stopErr error
	stop := func() error {
		stopOnce.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(10 * time.Second):
				stopErr = context.DeadlineExceeded
			}
			transport.CloseIdleConnections()
		})
		return stopErr
	}
	server := liveServer{url: fields[1], client: &http.Client{Transport: transport, Timeout: 5 * time.Second}, stop: stop}
	t.Cleanup(func() {
		if runErr := server.stop(); runErr != nil {
			t.Errorf("run shutdown: %v", runErr)
		}
	})
	return server
}

type liveRequest struct {
	method  string
	path    string
	token   string
	body    string
	headers map[string]string
}

func (server liveServer) request(t *testing.T, input liveRequest) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), input.method, server.url+input.path, bytes.NewBufferString(input.body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if input.body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if input.token != "" {
		request.Header.Set("Authorization", "Bearer "+input.token)
	}
	for name, value := range input.headers {
		request.Header.Set(name, value)
	}
	response, err := server.client.Do(request)
	if err != nil {
		t.Fatalf("request %s %s: %v", input.method, input.path, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return response, payload
}

func TestRun_catalog_roots_remain_authoritative_across_two_restarts_and_POST(t *testing.T) {
	// Given
	directory := t.TempDir()
	media := filepath.Join(directory, "media")
	left := filepath.Join(media, "left")
	right := filepath.Join(media, "right")
	added := filepath.Join(media, "added")
	for _, path := range []string{left, right, added} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	server := startLiveServer(t, directory)
	_, bootstrapBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/bootstrap", body: `{"setup_secret":"integration-setup","name":"Admin"}`})
	var credential struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(bootstrapBody, &credential); err != nil || credential.Token == "" {
		t.Fatalf("bootstrap = %s (%v)", bootstrapBody, err)
	}
	rootPatch := fmt.Sprintf(`{"catalog_roots":[{"id":"left","display_name":"Left","path":%q},{"id":"right","display_name":"Right","path":%q}]}`, left, right)
	patched, payload := server.request(t, liveRequest{
		method: http.MethodPatch, path: "/api/v1/config", token: credential.Token, body: rootPatch,
		headers: map[string]string{"If-Match": `"0"`, "Idempotency-Key": "exact-roots"},
	})
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("patch roots = %d %s", patched.StatusCode, payload)
	}
	if err := server.stop(); err != nil {
		t.Fatal(err)
	}

	// When
	firstRestart := startLiveServer(t, directory)
	configAfterFirst, configBody := firstRestart.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/config", token: credential.Token})
	rootsAfterFirst, rootsBody := firstRestart.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/catalog/roots", token: credential.Token})
	removal, removalBody := firstRestart.request(t, liveRequest{
		method: http.MethodPatch, path: "/api/v1/config", token: credential.Token,
		body:    fmt.Sprintf(`{"catalog_roots":[{"id":"right","display_name":"Right","path":%q}]}`, right),
		headers: map[string]string{"If-Match": `"1"`, "Idempotency-Key": "remove-left"},
	})
	post, postBody := firstRestart.request(t, liveRequest{
		method: http.MethodPost, path: "/api/v1/catalog/roots", token: credential.Token,
		body: fmt.Sprintf(`{"path":%q,"display_name":"Added"}`, added),
	})
	if err := firstRestart.stop(); err != nil {
		t.Fatal(err)
	}
	secondRestart := startLiveServer(t, directory)
	configAfterSecond, finalConfigBody := secondRestart.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/config", token: credential.Token})
	rootsAfterSecond, finalRootsBody := secondRestart.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/catalog/roots", token: credential.Token})
	document, documentErr := settings.LoadDocument(filepath.Join(directory, "config", "settings.json"))
	var finalConfig settings.Snapshot
	configDecodeErr := json.Unmarshal(finalConfigBody, &finalConfig)
	var finalRoots struct {
		Items []catalog.Root `json:"items"`
	}
	rootsDecodeErr := json.Unmarshal(finalRootsBody, &finalRoots)

	// Then
	if configAfterFirst.StatusCode != http.StatusOK || rootsAfterFirst.StatusCode != http.StatusOK || removal.StatusCode != http.StatusOK || post.StatusCode != http.StatusCreated ||
		configAfterSecond.StatusCode != http.StatusOK || rootsAfterSecond.StatusCode != http.StatusOK || documentErr != nil || configDecodeErr != nil || rootsDecodeErr != nil {
		t.Fatalf("statuses first=%d/%d removal=%d %s post=%d %s second=%d/%d decode=%v/%v/%v", configAfterFirst.StatusCode, rootsAfterFirst.StatusCode, removal.StatusCode, removalBody, post.StatusCode, postBody, configAfterSecond.StatusCode, rootsAfterSecond.StatusCode, documentErr, configDecodeErr, rootsDecodeErr)
	}
	if !strings.Contains(string(configBody), `"id":"left"`) || !strings.Contains(string(configBody), `"id":"right"`) || !strings.Contains(string(rootsBody), `"root_id":"left"`) || !strings.Contains(string(rootsBody), `"root_id":"right"`) {
		t.Fatalf("first restart disagreement: config=%s roots=%s", configBody, rootsBody)
	}
	if strings.Contains(string(finalConfigBody), `"id":"left"`) || strings.Contains(string(finalRootsBody), `"root_id":"left"`) || len(document.Settings.CatalogRoots) != 2 ||
		!reflect.DeepEqual(finalConfig.Settings.CatalogRoots, document.Settings.CatalogRoots) || len(finalRoots.Items) != len(document.Settings.CatalogRoots) {
		t.Fatalf("second restart disagreement: config=%s roots=%s settings=%+v", finalConfigBody, finalRootsBody, document.Settings.CatalogRoots)
	}
	for _, desired := range document.Settings.CatalogRoots {
		matched := false
		for _, projected := range finalRoots.Items {
			if string(projected.ID) == desired.ID && projected.DisplayName == desired.DisplayName {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("desired root missing from API: desired=%+v roots=%+v", desired, finalRoots.Items)
		}
	}
}

func TestRun_serves_integrated_config_catalog_and_zone_routes_across_restart(t *testing.T) {
	// Given
	directory := t.TempDir()
	server := startLiveServer(t, directory)
	health, _ := server.request(t, liveRequest{method: http.MethodGet, path: "/healthz"})
	_, bootstrapBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/bootstrap", body: `{"setup_secret":"integration-setup","name":"Admin"}`})
	var credential struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(bootstrapBody, &credential); err != nil || credential.Token == "" {
		t.Fatalf("bootstrap = %s (%v)", bootstrapBody, err)
	}
	configResponse, _ := server.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/config", token: credential.Token})
	patchResponse, patchPayload := server.request(t, liveRequest{
		method: http.MethodPatch, path: "/api/v1/config", token: credential.Token,
		body: `{"display_name":"Restarted Server"}`, headers: map[string]string{"If-Match": `"0"`, "Idempotency-Key": "rename"},
	})
	staleResponse, stalePayload := server.request(t, liveRequest{
		method: http.MethodPatch, path: "/api/v1/config", token: credential.Token,
		body: `{"display_name":"Stale Mutation"}`, headers: map[string]string{"If-Match": `"0"`, "Idempotency-Key": "stale"},
	})
	catalogResponse, _ := server.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/catalog/tracks", token: credential.Token})
	zoneResponse, _ := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/zones", token: credential.Token, body: `{"zone_id":"living","name":"Living room"}`})

	// When
	if runErr := server.stop(); runErr != nil {
		t.Fatalf("first shutdown: %v", runErr)
	}
	restarted := startLiveServer(t, directory)
	restartedConfig, restartedPayload := restarted.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/config", token: credential.Token})
	zones, _ := restarted.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/zones", token: credential.Token})

	// Then
	if health.StatusCode != http.StatusOK || configResponse.StatusCode != http.StatusOK || configResponse.Header.Get("ETag") != `"0"` ||
		patchResponse.StatusCode != http.StatusOK || !strings.Contains(string(patchPayload), `"revision":1`) ||
		staleResponse.StatusCode != http.StatusPreconditionFailed || !strings.Contains(string(stalePayload), "STALE_CONFIG_REVISION") ||
		catalogResponse.StatusCode != http.StatusOK || zoneResponse.StatusCode != http.StatusCreated ||
		restartedConfig.StatusCode != http.StatusOK || restartedConfig.Header.Get("ETag") != `"1"` ||
		!strings.Contains(string(restartedPayload), "Restarted Server") || zones.StatusCode != http.StatusOK {
		t.Fatalf("live statuses config=%d patch=%d stale=%d catalog=%d zone=%d restart=%d zones=%d", configResponse.StatusCode, patchResponse.StatusCode, staleResponse.StatusCode, catalogResponse.StatusCode, zoneResponse.StatusCode, restartedConfig.StatusCode, zones.StatusCode)
	}
}
