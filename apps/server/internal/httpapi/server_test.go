package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/auth"
	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/discovery"
	"github.com/jastreamer/jastreamer-server/internal/dlna"
	"github.com/jastreamer/jastreamer-server/internal/downloads"
	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/events"
	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/player"
	"github.com/jastreamer/jastreamer-server/internal/stream"
)

type apiFixture struct {
	server     *httptest.Server
	client     *http.Client
	metadata   discovery.Metadata
	config     config.Config
	configPath string
	history    *errorhistory.Service
	catalog    *library.Service
	db         *sql.DB
}

func startAPI(t *testing.T, secure bool) apiFixture {
	return startAPIWithRestart(t, secure, nil)
}

func startAPIWithRestart(t *testing.T, secure bool, restart *RestartHooks) apiFixture {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := database.Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	accounts, err := auth.New(db)
	if err != nil {
		t.Fatal(err)
	}
	discoveryService, err := discovery.New(ctx, db, "Test server", "test")
	if err != nil {
		t.Fatal(err)
	}
	hub := events.New()
	history, err := errorhistory.New(ctx, db, hub.Publish)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := library.New(ctx, db, nil, filepath.Join(dir, "artwork"), hub.Publish)
	if err != nil {
		t.Fatal(err)
	}
	downloadService, err := downloads.New(ctx, db, catalog, filepath.Join(dir, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(downloadService.Close)
	devices, err := dlna.New(dlna.Config{DiscoveryInterval: 30 * time.Second, Notify: hub.Publish})
	if err != nil {
		t.Fatal(err)
	}
	media, err := stream.New(catalog, stream.Config{BaseURL: func(output.Device) (string, error) { return "http://127.0.0.1:8080", nil }})
	if err != nil {
		t.Fatal(err)
	}
	outputs := output.NewManager(output.Backend{Protocol: output.ProtocolUPnP, Controller: devices, Media: media})
	playback, err := player.New(ctx, db, catalog, outputs, time.Second, hub.Publish, history)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = dir
	path := filepath.Join(dir, "server.json")
	if err = config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{Context: ctx, ConfigPath: path, Config: cfg, RuntimeID: "runtime_test", Restart: restart, Auth: accounts, Discovery: discoveryService, Library: catalog, Downloads: downloadService, History: history, Devices: outputs, Player: playback, Stream: media, Events: hub, UI: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Sign in</title>")}}})
	var server *httptest.Server
	if secure {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.Jar, _ = cookiejar.New(nil)
	return apiFixture{server: server, client: client, metadata: discoveryService.Metadata(), config: cfg, configPath: path, history: history, catalog: catalog, db: db}
}

func (fixture apiFixture) request(t *testing.T, method, path, body string, change func(*http.Request)) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, fixture.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal("request construction failed")
	}
	if method != "GET" {
		request.Header.Set("Origin", fixture.server.URL)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Jastreamer-Request", "web")
	}
	if change != nil {
		change(request)
	}
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatalf("HTTP request failed: %T", err)
	}
	return response
}

func expectStatus(t *testing.T, response *http.Response, status int) {
	t.Helper()
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != status {
		t.Fatalf("HTTP status=%d, want %d", response.StatusCode, status)
	}
}

const credentials = `{"username":"listener","password":"test-only-password-47"}`

func (fixture apiFixture) setup(t *testing.T) *http.Cookie {
	t.Helper()
	response := fixture.request(t, "POST", "/api/v1/setup", credentials, nil)
	cookies := response.Cookies()
	expectStatus(t, response, 201)
	for _, cookie := range cookies {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}
	t.Fatal("setup did not issue a session cookie")
	return nil
}

func (fixture apiFixture) addTestTrack(t *testing.T, id string, liked bool) {
	t.Helper()
	if _, err := fixture.db.ExecContext(t.Context(), `INSERT INTO library_tracks(id,root_id,relative_path,title,artist,album,album_artist,album_id,disc,track_number,genres_json,duration_ms,format,mime,artwork_id,byte_size,modified_ns,modified_at,available,last_seen_scan) VALUES(?,?,?,?,?,?,?,?,0,0,'[]',1000,'wav','audio/wav','',100,0,'2026-01-01T00:00:00Z',1,'scan')`, id, "root", id+".wav", id, "artist", "album", "artist", "album"); err != nil {
		t.Fatal(err)
	}
	if liked {
		if _, err := fixture.catalog.SetLiked(t.Context(), id, true); err != nil {
			t.Fatal(err)
		}
	}
}

func (fixture apiFixture) queueState(t *testing.T) player.Queue {
	t.Helper()
	response := fixture.request(t, http.MethodGet, "/api/v1/queue", "", nil)
	defer response.Body.Close()
	var queue player.Queue
	if err := json.NewDecoder(response.Body).Decode(&queue); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("queue HTTP status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	return queue
}

func (fixture apiFixture) appendTracks(t *testing.T, revision int64, trackIDs ...string) player.Queue {
	t.Helper()
	body, err := json.Marshal(player.QueueMutation{Action: "append", TrackIDs: trackIDs, Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	response := fixture.request(t, http.MethodPost, "/api/v1/queue", string(body), nil)
	defer response.Body.Close()
	var queue player.Queue
	if err = json.NewDecoder(response.Body).Decode(&queue); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("append HTTP status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	return queue
}

func TestAppendLikedShuffledPreservesQueueCursorDuplicatesAndSavedPlaylists(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	fixture.addTestTrack(t, "existing", false)
	fixture.addTestTrack(t, "liked-a", true)
	fixture.addTestTrack(t, "liked-b", true)
	saved, err := fixture.catalog.SavePlaylist(t.Context(), "", "Existing playlist", []string{"existing", "liked-a"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	initial := fixture.appendTracks(t, 0, "existing", "liked-a")
	currentEntryID := initial.Entries[1].ID
	if _, err = fixture.db.ExecContext(t.Context(), `UPDATE player_state SET state='paused',current_entry_id=?,position_ms=3210 WHERE singleton=1`, currentEntryID); err != nil {
		t.Fatal(err)
	}

	response := fixture.request(t, http.MethodPost, "/api/v1/queue", fmt.Sprintf(`{"action":"append_liked_shuffled","revision":%d}`, initial.Revision), nil)
	defer response.Body.Close()
	var result player.Queue
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("liked append status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	if len(result.Entries) != 4 || result.Revision != initial.Revision+1 {
		t.Fatalf("liked append queue=%#v", result)
	}
	for index := range initial.Entries {
		if result.Entries[index].ID != initial.Entries[index].ID || result.Entries[index].TrackID != initial.Entries[index].TrackID {
			t.Fatalf("existing queue order changed at %d: before=%#v after=%#v", index, initial.Entries[index], result.Entries[index])
		}
	}
	suffix := map[string]int{}
	for _, entry := range result.Entries[len(initial.Entries):] {
		suffix[entry.TrackID]++
	}
	if !reflect.DeepEqual(suffix, map[string]int{"liked-a": 1, "liked-b": 1}) {
		t.Fatalf("liked suffix=%v", suffix)
	}
	if result.Entries[1].TrackID != "liked-a" || result.Entries[1].ID == result.Entries[2].ID || result.Entries[1].ID == result.Entries[3].ID {
		t.Fatalf("pre-existing liked duplicate was not retained independently: %#v", result.Entries)
	}

	stateResponse := fixture.request(t, http.MethodGet, "/api/v1/player", "", nil)
	defer stateResponse.Body.Close()
	var state player.State
	if err = json.NewDecoder(stateResponse.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if stateResponse.StatusCode != http.StatusOK || state.State != player.StatePaused || state.CurrentEntryID != currentEntryID || state.PositionMS != 3210 {
		t.Fatalf("liked append changed playback: status=%d state=%#v", stateResponse.StatusCode, state)
	}
	playlists, err := fixture.catalog.Playlists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(playlists) != 1 || playlists[0].ID != saved.ID || !reflect.DeepEqual(playlists[0].TrackIDs, saved.TrackIDs) {
		t.Fatalf("liked append changed saved playlists: %#v", playlists)
	}
}

func TestAppendLikedShuffledEmptyAndStaleRequestsAreAtomic(t *testing.T) {
	t.Run("empty likes", func(t *testing.T) {
		fixture := startAPI(t, false)
		fixture.setup(t)
		fixture.addTestTrack(t, "existing", false)
		saved, err := fixture.catalog.SavePlaylist(t.Context(), "", "Existing playlist", []string{"existing"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		beforeQueue := fixture.appendTracks(t, 0, "existing")
		beforePlaylists, err := fixture.catalog.Playlists(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		response := fixture.request(t, http.MethodPost, "/api/v1/queue", fmt.Sprintf(`{"action":"append_liked_shuffled","revision":%d}`, beforeQueue.Revision), nil)
		status := response.StatusCode
		code := responseErrorCode(t, response)
		if status != http.StatusBadRequest || code != "INVALID_REQUEST" {
			t.Fatalf("empty likes status=%d code=%q", status, code)
		}
		afterQueue := fixture.queueState(t)
		afterPlaylists, err := fixture.catalog.Playlists(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(afterQueue, beforeQueue) || !reflect.DeepEqual(afterPlaylists, beforePlaylists) || len(afterPlaylists) != 1 || afterPlaylists[0].ID != saved.ID {
			t.Fatalf("empty liked append mutated state: queue before=%#v after=%#v playlists=%#v", beforeQueue, afterQueue, afterPlaylists)
		}
	})

	t.Run("stale revision", func(t *testing.T) {
		fixture := startAPI(t, false)
		fixture.setup(t)
		fixture.addTestTrack(t, "existing", false)
		fixture.addTestTrack(t, "liked", true)
		if _, err := fixture.catalog.SavePlaylist(t.Context(), "", "Existing playlist", []string{"liked"}, 0); err != nil {
			t.Fatal(err)
		}
		beforeQueue := fixture.appendTracks(t, 0, "existing")
		beforePlaylists, err := fixture.catalog.Playlists(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		response := fixture.request(t, http.MethodPost, "/api/v1/queue", `{"action":"append_liked_shuffled","revision":0}`, nil)
		status := response.StatusCode
		code := responseErrorCode(t, response)
		if status != http.StatusConflict || code != "REVISION_CONFLICT" {
			t.Fatalf("stale revision status=%d code=%q", status, code)
		}
		afterQueue := fixture.queueState(t)
		afterPlaylists, err := fixture.catalog.Playlists(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(afterQueue, beforeQueue) || !reflect.DeepEqual(afterPlaylists, beforePlaylists) {
			t.Fatalf("stale liked append mutated state: queue before=%#v after=%#v playlists before=%#v after=%#v", beforeQueue, afterQueue, beforePlaylists, afterPlaylists)
		}
	})
}

func TestAppendLikedShuffledCapacityFailureIsAtomic(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	fixture.addTestTrack(t, "existing", false)
	fixture.addTestTrack(t, "liked-a", true)
	fixture.addTestTrack(t, "liked-b", true)
	if _, err := fixture.catalog.SavePlaylist(t.Context(), "", "Existing playlist", []string{"existing"}, 0); err != nil {
		t.Fatal(err)
	}
	beforePlaylists, err := fixture.catalog.Playlists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const fillQueue = `WITH digits(d) AS (VALUES(0),(1),(2),(3),(4),(5),(6),(7),(8),(9)),
numbers(n) AS (SELECT ones.d + 10*tens.d + 100*hundreds.d + 1000*thousands.d FROM digits ones CROSS JOIN digits tens CROSS JOIN digits hundreds CROSS JOIN digits thousands ORDER BY 1 LIMIT 9999)
INSERT INTO player_queue(entry_id,position,track_id,status) SELECT printf('capacity-%04d',n),n,'existing','pending' FROM numbers`
	if _, err = fixture.db.ExecContext(t.Context(), fillQueue); err != nil {
		t.Fatal(err)
	}

	response := fixture.request(t, http.MethodPost, "/api/v1/queue", `{"action":"append_liked_shuffled","revision":0}`, nil)
	status := response.StatusCode
	code := responseErrorCode(t, response)
	if status != http.StatusConflict || code != "QUEUE_LIMIT" {
		t.Fatalf("capacity status=%d code=%q", status, code)
	}
	var entries, capacityEntries, maxPosition int
	if err = fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*),SUM(CASE WHEN entry_id LIKE 'capacity-%' THEN 1 ELSE 0 END),MAX(position) FROM player_queue`).Scan(&entries, &capacityEntries, &maxPosition); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = fixture.db.QueryRowContext(t.Context(), `SELECT queue_revision FROM player_state WHERE singleton=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	afterPlaylists, err := fixture.catalog.Playlists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if entries != 9999 || capacityEntries != 9999 || maxPosition != 9998 || revision != 0 || !reflect.DeepEqual(afterPlaylists, beforePlaylists) {
		t.Fatalf("capacity failure mutated state: entries=%d capacity=%d max=%d revision=%d playlists=%#v", entries, capacityEntries, maxPosition, revision, afterPlaylists)
	}
}

func TestDiscoveryIsPublicAndReturnsOnlyConnectionMetadata(t *testing.T) {
	fixture := startAPI(t, false)
	response := fixture.request(t, "GET", "/api/v1/discovery", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	expectedFields := []string{"product", "protocol", "id", "name", "version"}
	if len(fields) != len(expectedFields) {
		t.Fatalf("discovery response fields=%v", fields)
	}
	for _, field := range expectedFields {
		if _, exists := fields[field]; !exists {
			t.Fatalf("discovery response omitted %q: %v", field, fields)
		}
	}
	var metadata discovery.Metadata
	if err = json.Unmarshal(body, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != fixture.metadata {
		t.Fatalf("discovery metadata=%#v, want %#v", metadata, fixture.metadata)
	}
}

func TestLibraryPlayedQueryParsing(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	expectStatus(t, fixture.request(t, http.MethodGet, "/api/v1/library/tracks?played=true&sort=most_played", "", nil), http.StatusOK)
	response := fixture.request(t, http.MethodGet, "/api/v1/library/tracks?played=not-a-boolean", "", nil)
	if code := responseErrorCode(t, response); code != "INVALID_QUERY" {
		t.Fatalf("invalid played filter error=%q, want INVALID_QUERY", code)
	}
}

func TestLibraryRecursiveQueryParsingAndValidation(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	expectStatus(t, fixture.request(t, http.MethodGet, "/api/v1/library/tracks?root_id=music&recursive=true", "", nil), http.StatusOK)
	expectStatus(t, fixture.request(t, http.MethodGet, "/api/v1/library/albums?recursive=false", "", nil), http.StatusOK)
	for _, test := range []struct {
		path string
		code string
	}{
		{path: "/api/v1/library/tracks?root_id=music&recursive=not-a-boolean", code: "INVALID_QUERY"},
		{path: "/api/v1/library/tracks?recursive=true", code: "INVALID_REQUEST"},
		{path: "/api/v1/library/albums?root_id=music&recursive=true", code: "INVALID_REQUEST"},
	} {
		response := fixture.request(t, http.MethodGet, test.path, "", nil)
		if code := responseErrorCode(t, response); code != test.code {
			t.Fatalf("%s error=%q, want %s", test.path, code, test.code)
		}
	}
}

func TestLibraryScanModeRequestDefaultsAndValidation(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	for _, body := range []string{`{"mode":"quick"}`, `{"mode":""}`, `{"mode":null}`, `{"mode":3}`} {
		invalid := fixture.request(t, http.MethodPost, "/api/v1/library/scans", body, nil)
		if code := responseErrorCode(t, invalid); code != "INVALID_REQUEST" {
			t.Fatalf("invalid scan mode %s error=%q, want INVALID_REQUEST", body, code)
		}
	}

	waitForScan := func(id string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			jobs, err := fixture.catalog.Scans(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, job := range jobs {
				if job.ID == id && job.Status != "queued" && job.Status != "running" {
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("scan %q did not finish", id)
	}
	for _, request := range []struct {
		name string
		body string
	}{
		{name: "empty body"},
		{name: "empty object", body: `{}`},
		{name: "explicit incremental", body: `{"mode":"incremental"}`},
		{name: "explicit full", body: `{"mode":"full"}`},
	} {
		t.Run(request.name, func(t *testing.T) {
			response := fixture.request(t, http.MethodPost, "/api/v1/library/scans", request.body, nil)
			defer response.Body.Close()
			var job library.ScanJob
			if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusAccepted || job.ID == "" || job.Status != "queued" {
				t.Fatalf("start scan status=%d job=%+v", response.StatusCode, job)
			}
			waitForScan(job.ID)
		})
	}
}

func TestFirstSetupRejectsCrossOriginRebindingAndMissingCSRFHeader(t *testing.T) {
	fixture := startAPI(t, false)
	for _, attack := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"origin", func(request *http.Request) { request.Header.Set("Origin", "http://untrusted.example") }},
		{"host", func(request *http.Request) { request.Host = "untrusted.example"; request.Header.Del("Origin") }},
		{"header", func(request *http.Request) { request.Header.Del("X-Jastreamer-Request") }},
	} {
		t.Run(attack.name, func(t *testing.T) {
			expectStatus(t, fixture.request(t, "POST", "/api/v1/setup", credentials, attack.change), 403)
		})
	}
	response := fixture.request(t, "GET", "/api/v1/setup", "", nil)
	var state struct {
		Required bool `json:"required"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !state.Required {
		t.Fatal("rejected requests claimed the first account")
	}
	fixture.setup(t)
	expectStatus(t, fixture.request(t, "POST", "/api/v1/setup", credentials, nil), 409)
}

func TestSessionCookieFollowsHTTPAndHTTPSAndLogoutRevokesItsValue(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "HTTPS"}[secure], func(t *testing.T) {
			fixture := startAPI(t, secure)
			cookie := fixture.setup(t)
			if !cookie.HttpOnly || cookie.Secure != secure || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("session cookie transport or isolation flags are incorrect")
			}
			expectStatus(t, fixture.request(t, "GET", "/api/v1/queue", "", nil), 200)
			expectStatus(t, fixture.request(t, "POST", "/api/v1/logout", "{}", nil), 204)
			expectStatus(t, fixture.request(t, "GET", "/api/v1/queue", "", func(request *http.Request) { request.AddCookie(cookie) }), 401)
			// An expired/revoked cookie must not prevent a fresh password login.
			expectStatus(t, fixture.request(t, "POST", "/api/v1/login", credentials, func(request *http.Request) { request.AddCookie(cookie) }), 200)
			expectStatus(t, fixture.request(t, "GET", "/api/v1/queue", "", nil), 200)
		})
	}
}

func TestLogoutClosesAnAlreadyAuthenticatedEventStream(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	response := fixture.request(t, "GET", "/api/v1/events", "", nil)
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("SSE status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("initial SSE failed: %T", err)
		}
		if line == "\n" {
			break
		}
	}
	expectStatus(t, fixture.request(t, "POST", "/api/v1/logout", "{}", nil), 204)
	finished := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, reader); finished <- err }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("revoked stream did not end cleanly: %T", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("logout left an authenticated event stream open")
	}
}

func TestPairingRouteRequiresAuthenticationAndUsesOutputLookup(t *testing.T) {
	fixture := startAPI(t, false)
	path := "/api/v1/renderers/missing/pairing"
	expectStatus(t, fixture.request(t, "POST", path, `{"pin":"1234","password":""}`, nil), http.StatusUnauthorized)
	fixture.setup(t)
	response := fixture.request(t, "POST", path, `{"pin":"1234","password":""}`, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("pairing status=%d, want %d", response.StatusCode, http.StatusNotFound)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "OUTPUT_NOT_FOUND" {
		t.Fatalf("pairing route error=%q, want OUTPUT_NOT_FOUND", body.Error.Code)
	}
}

func TestIncompleteJSONBodyIsBoundedWithoutBlockingOtherRequests(t *testing.T) {
	fixture := startAPI(t, false)
	address := strings.TrimPrefix(fixture.server.URL, "http://")
	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err = connection.SetDeadline(time.Now().Add(requestBodyTimeout + 2*time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(connection, "POST /api/v1/login HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nX-Jastreamer-Request: web\r\nContent-Length: 200\r\nConnection: close\r\n\r\n{", address)
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, fixture.request(t, "GET", "/healthz", "", nil), 200)
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: "POST"})
	if err != nil {
		t.Fatalf("incomplete request did not receive a bounded response: %T", err)
	}
	expectStatus(t, response, http.StatusRequestTimeout)
	expectStatus(t, fixture.request(t, "GET", "/api/v1/setup", "", nil), 200)
}

func TestConfigAdvertisesPendingRestartAfterReload(t *testing.T) {
	fixture := startAPIWithRestart(t, false, &RestartHooks{
		Prepare: func(context.Context, config.Config) error { return nil },
		Commit:  func(RestartRequest) {},
	})
	fixture.setup(t)
	response := fixture.request(t, http.MethodGet, "/api/v1/config", "", nil)
	var initial struct {
		Config           config.Config `json:"config"`
		Revision         string        `json:"revision"`
		RestartRequired  bool          `json:"restart_required"`
		RestartSupported bool          `json:"restart_supported"`
		RuntimeID        string        `json:"runtime_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&initial); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || initial.RestartRequired || !initial.RestartSupported || initial.RuntimeID != "runtime_test" {
		t.Fatalf("initial restart state is wrong: status=%d state=%+v", response.StatusCode, initial)
	}
	initial.Config.ServerName = "Restarted server"
	payload, err := json.Marshal(map[string]any{"config": initial.Config, "revision": initial.Revision})
	if err != nil {
		t.Fatal(err)
	}
	response = fixture.request(t, http.MethodPut, "/api/v1/config", string(payload), nil)
	var saved struct {
		Revision        string `json:"revision"`
		RestartRequired bool   `json:"restart_required"`
	}
	if err = json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !saved.RestartRequired || saved.Revision == initial.Revision {
		t.Fatalf("saved restart state is wrong: status=%d state=%+v", response.StatusCode, saved)
	}
	response = fixture.request(t, http.MethodGet, "/api/v1/config", "", nil)
	var reloaded struct {
		Revision        string `json:"revision"`
		RestartRequired bool   `json:"restart_required"`
	}
	if err = json.NewDecoder(response.Body).Decode(&reloaded); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !reloaded.RestartRequired || reloaded.Revision != saved.Revision {
		t.Fatalf("GET lost the pending restart: status=%d state=%+v", response.StatusCode, reloaded)
	}
}

func TestRestartRequiresAuthenticationGuardsAndCurrentRevision(t *testing.T) {
	prepared := 0
	fixture := startAPIWithRestart(t, false, &RestartHooks{
		Prepare: func(context.Context, config.Config) error {
			prepared++
			return nil
		},
		Commit: func(RestartRequest) {},
	})
	expectStatus(t, fixture.request(t, http.MethodPost, "/api/v1/restart", `{"revision":"stale"}`, nil), http.StatusUnauthorized)
	fixture.setup(t)
	expectStatus(t, fixture.request(t, http.MethodPost, "/api/v1/restart", `{"revision":"stale"}`, func(request *http.Request) {
		request.Header.Set("Origin", "http://untrusted.example")
	}), http.StatusForbidden)
	target := fixture.config
	target.ServerName = "Changed"
	if err := config.Save(fixture.configPath, target); err != nil {
		t.Fatal(err)
	}
	response := fixture.request(t, http.MethodPost, "/api/v1/restart", `{"revision":"stale"}`, nil)
	if code := responseErrorCode(t, response); code != "CONFIG_CONFLICT" {
		t.Fatalf("stale restart error=%q, want CONFIG_CONFLICT", code)
	}
	target.DataDir = filepath.Join(filepath.Dir(fixture.configPath), "moved-data")
	if err := config.Save(fixture.configPath, target); err != nil {
		t.Fatal(err)
	}
	response = fixture.request(t, http.MethodPost, "/api/v1/restart", `{"revision":"`+configRevision(target)+`"}`, nil)
	if code := responseErrorCode(t, response); code != "DATA_DIR_MIGRATION_REQUIRED" {
		t.Fatalf("data directory restart error=%q, want DATA_DIR_MIGRATION_REQUIRED", code)
	}
	if prepared != 0 {
		t.Fatal("rejected restart reached lifecycle preparation")
	}
}

func TestFailedRestartPreparationLeavesRuntimeMutable(t *testing.T) {
	commits := 0
	fixture := startAPIWithRestart(t, false, &RestartHooks{
		Prepare: func(context.Context, config.Config) error {
			return fault.New(http.StatusConflict, "RESTART_STOP_FAILED", "renderer transport refused Stop")
		},
		Commit: func(RestartRequest) { commits++ },
	})
	fixture.setup(t)
	target := fixture.config
	target.ServerName = "Changed"
	if err := config.Save(fixture.configPath, target); err != nil {
		t.Fatal(err)
	}
	revision := configRevision(target)
	response := fixture.request(t, http.MethodPost, "/api/v1/restart", `{"revision":"`+revision+`"}`, nil)
	if code := responseErrorCode(t, response); code != "RESTART_STOP_FAILED" {
		t.Fatalf("failed preparation error=%q, want RESTART_STOP_FAILED", code)
	}
	if commits != 0 {
		t.Fatal("failed preparation committed a restart")
	}
	payload, err := json.Marshal(map[string]any{"config": target, "revision": revision})
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, fixture.request(t, http.MethodPut, "/api/v1/config", string(payload), nil), http.StatusOK)
	expectStatus(t, fixture.request(t, http.MethodGet, "/healthz", "", nil), http.StatusOK)
}

func TestAcceptedRestartRejectsDuplicateAndReturnsOldRuntime(t *testing.T) {
	fixture := startAPIWithRestart(t, false, &RestartHooks{
		Prepare: func(context.Context, config.Config) error { return nil },
		Commit:  func(RestartRequest) {},
	})
	fixture.setup(t)
	target := fixture.config
	target.HTTP.Address = "127.0.0.1:9090"
	if err := config.Save(fixture.configPath, target); err != nil {
		t.Fatal(err)
	}
	revision := configRevision(target)
	response := fixture.request(t, http.MethodPost, "/api/v1/restart", `{"revision":"`+revision+`"}`, nil)
	var accepted struct {
		RuntimeID    string `json:"runtime_id"`
		ReconnectURL string `json:"reconnect_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted || accepted.RuntimeID != "runtime_test" || accepted.ReconnectURL != "http://127.0.0.1:9090" {
		t.Fatalf("restart acceptance is wrong: status=%d body=%+v", response.StatusCode, accepted)
	}
	response = fixture.request(t, http.MethodPost, "/api/v1/restart", `{"revision":"`+revision+`"}`, nil)
	if code := responseErrorCode(t, response); code != "RESTART_IN_PROGRESS" {
		t.Fatalf("duplicate restart error=%q, want RESTART_IN_PROGRESS", code)
	}
	response = fixture.request(t, http.MethodPost, "/api/v1/library/scans", `{}`, nil)
	if code := responseErrorCode(t, response); code != "RESTART_IN_PROGRESS" {
		t.Fatalf("mutation admitted after restart acceptance: %q", code)
	}
}

func responseErrorCode(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Error.Code
}

func TestRestartURLPreservesAdmittedIPv6AndUsesChangedSpecificBind(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://[fd00::10]:8080/api/v1/restart", nil)
	request.Host = "[fd00::10]:8080"
	active := config.Default()
	target := active
	target.HTTP.Enabled = false
	target.HTTPS.Enabled = true
	target.HTTPS.Address = "[::]:8443"
	url, err := restartURL(request, active, target)
	if err != nil || url != "https://[fd00::10]:8443" {
		t.Fatalf("wildcard target did not preserve the admitted IPv6 host: url=%q err=%v", url, err)
	}
	target.HTTPS.Address = "[fd00::20]:8443"
	url, err = restartURL(request, active, target)
	if err != nil || url != "https://[fd00::20]:8443" {
		t.Fatalf("changed specific bind was not used: url=%q err=%v", url, err)
	}
}

func TestConfigRestartStateDistinguishesLiveAndExternalRoots(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	response := fixture.request(t, http.MethodGet, "/api/v1/config", "", nil)
	var state struct {
		Config          config.Config `json:"config"`
		Revision        string        `json:"revision"`
		RestartRequired bool          `json:"restart_required"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	liveRoot := filepath.Join(filepath.Dir(fixture.configPath), "live-music")
	if err := os.Mkdir(liveRoot, 0700); err != nil {
		t.Fatal(err)
	}
	state.Config.LibraryRoots = []config.Root{{ID: "live", Name: "Live", Path: liveRoot}}
	payload, err := json.Marshal(map[string]any{"config": state.Config, "revision": state.Revision})
	if err != nil {
		t.Fatal(err)
	}
	response = fixture.request(t, http.MethodPut, "/api/v1/config", string(payload), nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("hot-apply status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	if err = json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if state.RestartRequired {
		t.Fatal("hot-applied library roots incorrectly require a restart")
	}
	externalRoot := filepath.Join(filepath.Dir(fixture.configPath), "external-music")
	if err = os.Mkdir(externalRoot, 0700); err != nil {
		t.Fatal(err)
	}
	state.Config.LibraryRoots = append(state.Config.LibraryRoots, config.Root{ID: "external", Name: "External", Path: externalRoot})
	if err = config.Save(fixture.configPath, state.Config); err != nil {
		t.Fatal(err)
	}
	response = fixture.request(t, http.MethodGet, "/api/v1/config", "", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reloaded config status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	if err = json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !state.RestartRequired {
		t.Fatal("GET treated externally changed roots as already live-applied")
	}
}

func TestHistoryAndVerificationRequireAuthentication(t *testing.T) {
	fixture := startAPI(t, false)
	expectStatus(t, fixture.request(t, "GET", "/api/v1/history", "", nil), http.StatusUnauthorized)
	expectStatus(t, fixture.request(t, "GET", "/api/v1/history/export", "", nil), http.StatusUnauthorized)
	expectStatus(t, fixture.request(t, "GET", "/api/v1/library/verification", "", nil), http.StatusUnauthorized)
}

func TestHistoryFiltersPagesAndNeverReturnsNullArrays(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	now := time.Now().UTC()
	historyEvents := []errorhistory.Event{
		{Key: "renderer-one", ReceivedAt: now, Kind: "renderer", RendererID: "renderer-1", RendererName: "Room", Protocol: "upnp", Stage: "Play", Code: "transport", Message: "Renderer failed.", Outcome: "unknown", Details: json.RawMessage(`{"category":"transport"}`)},
		{Key: "integrity-one", ReceivedAt: now.Add(time.Second), Kind: "integrity", TrackID: "track-1", TrackTitle: "Song", RootName: "Music", RelativePath: "album/song.flac", Stage: "decode", Code: "invalid_data", Message: "Audio verification failed.", Outcome: "failed", Details: json.RawMessage(`{"engine":"ffmpeg"}`)},
	}
	for _, event := range historyEvents {
		if err := fixture.history.Record(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	response := fixture.request(t, "GET", "/api/v1/history?kind=renderer&renderer_id=renderer-1&offset=0&limit=1", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	var result errorhistory.ListResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].Kind != "renderer" || len(result.Renderers) != 1 || result.RetentionLimit != errorhistory.RetentionLimit {
		t.Fatalf("history response=%#v", result)
	}
	empty := fixture.request(t, "GET", "/api/v1/history?kind=renderer&renderer_id=missing", "", nil)
	defer empty.Body.Close()
	var emptyResult errorhistory.ListResult
	if err := json.NewDecoder(empty.Body).Decode(&emptyResult); err != nil {
		t.Fatal(err)
	}
	if emptyResult.Items == nil || emptyResult.Renderers == nil {
		t.Fatalf("history arrays must not be null: %#v", emptyResult)
	}
}

func TestHistoryExportFiltersEscapesAndNeutralizesCSV(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	position := int64(1234)
	events := []errorhistory.Event{
		{Key: "matching-older", ReceivedAt: base, Kind: "renderer", RendererID: "renderer-1", RendererName: "Room", Protocol: "upnp", TrackID: "track-older", TrackTitle: "Older", Stage: "Play", Code: "transport", Message: "Older failure.", Outcome: "unknown", Details: json.RawMessage(`{"attempt":1}`)},
		{Key: "matching-newer", ReceivedAt: base.Add(time.Second), Kind: "renderer", RendererID: "renderer-1", RendererName: "\t@SUM(1,1)", Protocol: "upnp", TrackID: "+track", TrackTitle: "=HYPERLINK(\"https://example.invalid\")", RootName: " 음악,보관함", RelativePath: "=2+2,\"앨범\"\n곡.flac", Stage: " \t-CMD()", Code: "\u200e=CMD()", Message: "\r=cmd|' /C calc'!A0\n한국어", Outcome: "failed", PlayID: "@play", CommandID: " +command", PositionMS: &position, Details: json.RawMessage(`{"formula":"=SUM(1,1)","note":"한국어, \"quoted\""}`)},
		{Key: "other-renderer", ReceivedAt: base.Add(2 * time.Second), Kind: "renderer", RendererID: "renderer-2", Outcome: "unknown", Message: "Excluded renderer.", Details: json.RawMessage(`{}`)},
		{Key: "integrity", ReceivedAt: base.Add(3 * time.Second), Kind: "integrity", TrackID: "track-integrity", Outcome: "failed", Message: "Excluded file.", Details: json.RawMessage(`{}`)},
	}
	for _, event := range events {
		if err := fixture.history.Record(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}

	expectStatus(t, fixture.request(t, "GET", "/api/v1/history/export?kind=invalid", "", nil), http.StatusBadRequest)
	expectStatus(t, fixture.request(t, "GET", "/api/v1/history/export?renderer_id=%00", "", nil), http.StatusBadRequest)
	response := fixture.request(t, "GET", "/api/v1/history/export?kind=renderer&renderer_id=renderer-1", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	if response.Header.Get("Content-Type") != "text/csv; charset=utf-8" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("export headers=%v", response.Header)
	}
	if disposition := response.Header.Get("Content-Disposition"); disposition != `attachment; filename="jastreamer-diagnostic-history.csv"` {
		t.Fatalf("Content-Disposition=%q", disposition)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatalf("CSV omitted UTF-8 BOM: %x", body[:min(len(body), 3)])
	}
	if response.ContentLength != int64(len(body)) {
		t.Fatalf("Content-Length=%d, body=%d", response.ContentLength, len(body))
	}
	records, err := csv.NewReader(bytes.NewReader(body[3:])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("CSV rows=%d, want header plus 2 matching records", len(records))
	}
	headerIndex := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		headerIndex[name] = index
	}
	newer := records[1]
	older := records[2]
	if newer[headerIndex["track_title"]] != `'=HYPERLINK("https://example.invalid")` || older[headerIndex["track_title"]] != "Older" {
		t.Fatalf("CSV order or title escaping failed: newer=%q older=%q", newer, older)
	}
	expected := map[string]string{
		"track_id":      "'+track",
		"folder":        " 음악,보관함",
		"relative_path": "'=2+2,\"앨범\"\n곡.flac",
		"stage":         "' \t-CMD()",
		"code":          "'\u200e=CMD()",
		"message":       "'\r=cmd|' /C calc'!A0\n한국어",
		"renderer_name": "'\t@SUM(1,1)",
		"position_ms":   "1234",
		"play_id":       "'@play",
		"command_id":    "' +command",
		"details_json":  `{"formula":"=SUM(1,1)","note":"한국어, \"quoted\""}`,
	}
	for column, want := range expected {
		if got := newer[headerIndex[column]]; got != want {
			t.Fatalf("%s=%q, want %q", column, got, want)
		}
	}
}

func TestAuthenticatedVerificationStatus(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	response := fixture.request(t, "GET", "/api/v1/library/verification", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	var status library.VerificationStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.State == "" || status.Pending < 0 || status.Verified < 0 || status.Failed < 0 || status.Unverified < 0 {
		t.Fatalf("verification status=%#v", status)
	}
}
