package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

const task19TrackCount = 100_000

type task19MediaManifest struct {
	Count         int      `json:"count"`
	SeedSHA256    string   `json:"seed_sha256"`
	LogicalBytes  int64    `json:"logical_bytes"`
	PhysicalBytes int64    `json:"physical_bytes"`
	GenerationMS  float64  `json:"generation_ms"`
	Strategies    []string `json:"strategies"`
}

type task19EventSocket struct {
	connection net.Conn
	reader     *bufio.Reader
}

func task19Generator(t *testing.T, ctx context.Context, operation, root string, count int) task19MediaManifest {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "tooling", "qa", "task19", "task19-media-fixture.py"))
	if err != nil {
		t.Fatalf("resolve Task19 generator: %v", err)
	}
	command := exec.CommandContext(ctx, "python3", script, operation, "--root", root, "--count", fmt.Sprint(count), "--strategy", "auto")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Task19 generator %s: %v: %s", operation, err, output)
	}
	var manifest task19MediaManifest
	if err := json.Unmarshal(output, &manifest); err != nil {
		t.Fatalf("decode Task19 generator output %q: %v", output, err)
	}
	return manifest
}

func openTask19EventSocket(t *testing.T, server liveServer, token string) *task19EventSocket {
	t.Helper()
	_, ticketBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/event-tickets", token: token})
	var ticket struct {
		Value string `json:"ticket"`
	}
	if err := json.Unmarshal(ticketBody, &ticket); err != nil || ticket.Value == "" {
		t.Fatalf("issue event ticket = %s, %v", ticketBody, err)
	}
	parsed, err := url.Parse(server.url)
	if err != nil {
		t.Fatalf("parse Server URL: %v", err)
	}
	roots := x509.NewCertPool()
	connection, err := tls.Dial("tcp", parsed.Host, &tls.Config{RootCAs: roots, InsecureSkipVerify: true, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatalf("dial event stream: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	key := base64.StdEncoding.EncodeToString([]byte("task19-websocket"))
	if _, err := fmt.Fprintf(connection, "GET /api/v1/events?ticket=%s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", ticket.Value, parsed.Host, key); err != nil {
		t.Fatalf("write event upgrade: %v", err)
	}
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	if err != nil || status != "HTTP/1.1 101 Switching Protocols\r\n" {
		t.Fatalf("event upgrade = %q, %v", status, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read event header: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	socket := &task19EventSocket{connection: connection, reader: reader}
	initial := socket.readEvent(t, t.Context())
	if initial.Type != "snapshot" {
		t.Fatalf("initial event = %+v", initial)
	}
	return socket
}

type task19Event struct {
	Type     string `json:"type"`
	Resource string `json:"resource"`
	ZoneID   string `json:"zone_id"`
	Revision uint64 `json:"revision"`
	Sequence uint64 `json:"sequence"`
}

func (socket *task19EventSocket) readEvent(t *testing.T, ctx context.Context) task19Event {
	t.Helper()
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if err := socket.connection.SetReadDeadline(deadline); err != nil {
				t.Fatalf("bound event read: %v", err)
			}
		}
		opcode, payload, err := readTask19Frame(socket.reader)
		if err != nil {
			t.Fatalf("read event frame: %v", err)
		}
		if opcode == 0x9 {
			if err := writeTask19ClientFrame(socket.connection, 0xa, payload); err != nil {
				t.Fatalf("write event pong: %v", err)
			}
			continue
		}
		if opcode != 0x1 {
			t.Fatalf("event opcode = %#x", opcode)
		}
		var event task19Event
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode event %q: %v", payload, err)
		}
		return event
	}
}

func (socket *task19EventSocket) awaitCatalogScan(t *testing.T, ctx context.Context, after uint64) task19Event {
	t.Helper()
	for {
		event := socket.readEvent(t, ctx)
		if event.Type == "invalidation" && event.Resource == "catalog_scan" && event.Revision > after {
			return event
		}
	}
}

func readTask19Frame(reader *bufio.Reader) (byte, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, nil, err
	}
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var encoded [2]byte
		if _, err := io.ReadFull(reader, encoded[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(encoded[:]))
	case 127:
		var encoded [8]byte
		if _, err := io.ReadFull(reader, encoded[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(encoded[:])
	}
	payload := make([]byte, length)
	_, err := io.ReadFull(reader, payload)
	return header[0] & 0x0f, payload, err
}

func writeTask19ClientFrame(connection net.Conn, opcode byte, payload []byte) error {
	mask := [4]byte{1, 2, 3, 4}
	header := []byte{0x80 | opcode, 0x80 | byte(len(payload))}
	header = append(header, mask[:]...)
	masked := make([]byte, len(payload))
	for index := range payload {
		masked[index] = payload[index] ^ mask[index%len(mask)]
	}
	_, err := connection.Write(append(header, masked...))
	return err
}

type task19BrowseTrack struct {
	TrackID string `json:"track_id"`
	Format  string `json:"format"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	Album   string `json:"album"`
}

type task19Representatives struct {
	First  task19BrowseTrack
	Middle task19BrowseTrack
	Last   task19BrowseTrack
}

func browseTask19Catalog(t *testing.T, server liveServer, token string) (int, task19Representatives) {
	t.Helper()
	count := 0
	cursor := ""
	ids := make(map[string]struct{}, task19TrackCount)
	result := task19Representatives{}
	for {
		path := "/api/v1/catalog/tracks?limit=500"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		response, body := server.request(t, liveRequest{method: http.MethodGet, path: path, token: token})
		var page struct {
			Tracks     []task19BrowseTrack `json:"tracks"`
			NextCursor string              `json:"next_cursor"`
		}
		if err := json.Unmarshal(body, &page); err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("browse page = %d %s, %v", response.StatusCode, body, err)
		}
		for _, track := range page.Tracks {
			if track.TrackID == "" || track.Format != "pcm-wav" || track.Title != "Task19 deterministic audio" || track.Artist != "jastreamer Task19" || track.Album != "Task19 qualification" {
				t.Fatalf("invalid indexed track at %d = %+v", count, track)
			}
			if _, duplicate := ids[track.TrackID]; duplicate {
				t.Fatalf("duplicate indexed track ID %q", track.TrackID)
			}
			ids[track.TrackID] = struct{}{}
			switch count {
			case 0:
				result.First = track
			case task19TrackCount / 2:
				result.Middle = track
			case task19TrackCount - 1:
				result.Last = track
			}
			count++
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(ids) != count {
		t.Fatalf("catalog IDs=%d count=%d", len(ids), count)
	}
	return count, result
}

func TestTask19LiveServerScansExactly100000ValidMediaPaths(t *testing.T) {
	deadline, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	directory := t.TempDir()
	catalogBase := filepath.Join(directory, "media")
	catalogRoot := filepath.Join(catalogBase, "task19")
	if err := os.MkdirAll(catalogBase, 0o750); err != nil {
		t.Fatalf("create catalog base: %v", err)
	}
	config := serverConfig{
		address: "127.0.0.1:0", dataDirectory: directory, catalogRoot: catalogBase,
		catalogMigrationPath: "../../migrations/001_catalog.sql", playbackMigrationPath: "../../migrations/002_playback.sql", playbackExpansionPath: "../../migrations/003_todo12.sql",
		setupSecret: "integration-setup", certificateDNS: []string{"localhost"}, certificateIPs: []net.IP{net.ParseIP("127.0.0.1")}, pairingTTL: 5 * time.Minute,
	}
	settingsConfig, err := runtimeSettingsConfig(config, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("prepare settings config: %v", err)
	}
	settingsStore, err := settings.Open(settingsConfig)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	emptyRoots := []settings.CatalogRoot{}
	if _, err := settingsStore.Patch(deadline, settings.Mutation{ExpectedRevision: 0, IdempotencyKey: "task19-empty-roots", Update: settings.Update{CatalogRoots: &emptyRoots}}); err != nil {
		t.Fatalf("remove default root before startup: %v", err)
	}
	manifest := task19Generator(t, deadline, "create", catalogRoot, task19TrackCount)
	if manifest.Count != task19TrackCount || manifest.PhysicalBytes >= manifest.LogicalBytes || len(manifest.SeedSHA256) != 64 {
		t.Fatalf("media manifest = %+v", manifest)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = task19Generator(t, cleanupContext, "cleanup", catalogRoot, task19TrackCount)
	})

	server := startLiveServerConfig(t, config)
	_, bootstrapBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/bootstrap", body: `{"setup_secret":"integration-setup","name":"Admin"}`})
	var admin struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(bootstrapBody, &admin); err != nil || admin.Token == "" {
		t.Fatalf("bootstrap = %s, %v", bootstrapBody, err)
	}
	_, codeBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/pairing-codes", token: admin.Token, body: `{"role":"controller"}`})
	var code struct {
		Value string `json:"code"`
	}
	if err := json.Unmarshal(codeBody, &code); err != nil || code.Value == "" {
		t.Fatalf("pairing code = %s, %v", codeBody, err)
	}
	_, credentialBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/pairings", body: fmt.Sprintf(`{"code":%q,"name":"Task19 Control"}`, code.Value)})
	var controller struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(credentialBody, &controller); err != nil || controller.Token == "" {
		t.Fatalf("controller credential = %s, %v", credentialBody, err)
	}
	rootResponse, rootBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/catalog/roots", token: admin.Token, body: fmt.Sprintf(`{"path":%q,"display_name":"Task19 deterministic media"}`, catalogRoot)})
	var root catalog.Root
	if err := json.Unmarshal(rootBody, &root); err != nil || rootResponse.StatusCode != http.StatusCreated || root.ID == "" {
		t.Fatalf("add catalog root = %d %s, %v", rootResponse.StatusCode, rootBody, err)
	}
	events := openTask19EventSocket(t, server, controller.Token)

	scanStarted := time.Now()
	scanResponse, scanBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/catalog/scans", token: admin.Token, body: fmt.Sprintf(`{"root_id":%q}`, root.ID)})
	var scanJob struct {
		ID string `json:"job_id"`
	}
	if err := json.Unmarshal(scanBody, &scanJob); err != nil || scanResponse.StatusCode != http.StatusAccepted || scanJob.ID == "" {
		t.Fatalf("start scan = %d %s, %v", scanResponse.StatusCode, scanBody, err)
	}
	firstCompletion := events.awaitCatalogScan(t, deadline, 0)
	scanDuration := time.Since(scanStarted)
	statusResponse, statusBody := server.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/catalog/status", token: controller.Token})
	var status struct {
		TrackCount      int    `json:"track_count"`
		CatalogRevision uint64 `json:"catalog_revision"`
	}
	if err := json.Unmarshal(statusBody, &status); err != nil || statusResponse.StatusCode != http.StatusOK || status.TrackCount != task19TrackCount {
		t.Fatalf("catalog status = %d %s, %v", statusResponse.StatusCode, statusBody, err)
	}
	count, representatives := browseTask19Catalog(t, server, controller.Token)
	if count != task19TrackCount || representatives.First.TrackID == "" || representatives.Middle.TrackID == "" || representatives.Last.TrackID == "" {
		t.Fatalf("catalog count/representatives = %d %+v", count, representatives)
	}

	secondScanStarted := time.Now()
	secondResponse, secondBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/catalog/scans", token: admin.Token, body: fmt.Sprintf(`{"root_id":%q}`, root.ID)})
	if secondResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("second scan = %d %s", secondResponse.StatusCode, secondBody)
	}
	_ = events.awaitCatalogScan(t, deadline, firstCompletion.Revision)
	secondScanDuration := time.Since(secondScanStarted)
	secondCount, stable := browseTask19Catalog(t, server, controller.Token)
	if secondCount != task19TrackCount || stable != representatives {
		t.Fatalf("stable scan IDs changed: first=%+v second=%+v count=%d", representatives, stable, secondCount)
	}

	t.Logf("task19_media count=%d generation=%s scan=%s stable_rescan=%s logical_bytes=%d physical_bytes=%d strategy=%v first=%s middle=%s last=%s revision=%d", count, time.Duration(manifest.GenerationMS*float64(time.Millisecond)), scanDuration, secondScanDuration, manifest.LogicalBytes, manifest.PhysicalBytes, manifest.Strategies, representatives.First.TrackID, representatives.Middle.TrackID, representatives.Last.TrackID, status.CatalogRevision)
	if err := server.stop(); err != nil {
		t.Fatalf("stop Server: %v", err)
	}
	_ = task19Generator(t, deadline, "cleanup", catalogRoot, task19TrackCount)
	if err := os.RemoveAll(directory); err != nil {
		t.Fatalf("remove Task19 data root: %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("Task19 residue remains: %v", err)
	}
}
