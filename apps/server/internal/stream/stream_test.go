package stream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

const ffmpegHelperEnvironment = "JASTREAMER_STREAM_TEST_FFMPEG"

func TestMain(m *testing.M) {
	switch os.Getenv(ffmpegHelperEnvironment) {
	case "copy":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		os.Exit(0)
	case "wait":
		_, _ = os.Stdout.Write([]byte{'x'})
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

type fixtureLibrary struct {
	path        string
	artworkPath string
	artworkMIME string
	track       library.Track
	audio       library.AudioProperties
	mu          sync.Mutex
	opens       int
}

func (fixture *fixtureLibrary) Open(ctx context.Context, id string) (*os.File, library.Track, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, library.Track{}, err
	}
	fixture.mu.Lock()
	fixture.opens++
	track := fixture.track
	fixture.mu.Unlock()
	if id != track.ID {
		return nil, library.Track{}, os.ErrNotExist
	}
	file, err := os.Open(fixture.path)
	return file, track, err
}

func (fixture *fixtureLibrary) Artwork(ctx context.Context, id string) (*os.File, string, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, "", err
	}
	fixture.mu.Lock()
	track := fixture.track
	path := fixture.artworkPath
	mime := fixture.artworkMIME
	fixture.mu.Unlock()
	if id == "" || id != track.ArtworkID || path == "" {
		return nil, "", os.ErrNotExist
	}
	file, err := os.Open(path)
	return file, mime, err
}

func (fixture *fixtureLibrary) Info(ctx context.Context, id string) (library.TrackInfo, error) {
	if err := context.Cause(ctx); err != nil {
		return library.TrackInfo{}, err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if id != fixture.track.ID {
		return library.TrackInfo{}, os.ErrNotExist
	}
	return library.TrackInfo{Track: fixture.track, Audio: fixture.audio}, nil
}

func pointer[T any](value T) *T {
	return &value
}

func (fixture *fixtureLibrary) setArtwork(id, path, mime string) {
	fixture.mu.Lock()
	fixture.track.ArtworkID = id
	fixture.artworkPath = path
	fixture.artworkMIME = mime
	fixture.mu.Unlock()
}

func (fixture *fixtureLibrary) openCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.opens
}

func newOriginalFixture(t *testing.T, content []byte) (*Service, *fixtureLibrary, output.Device, library.Track) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "track.flac")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	track := library.Track{
		ID: "track-private-identity", Title: "Title", Artist: "Artist", Album: "Album",
		DurationMS: 90_000, Format: "flac", Mime: "audio/flac", Available: true,
		Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
	}
	fixture := &fixtureLibrary{
		path:  path,
		track: track,
		audio: library.AudioProperties{
			Codec: "FLAC", SampleRate: pointer[int64](44_100), Channels: pointer(2), BitsPerSample: pointer(16),
		},
	}
	service, err := newService(fixture, Config{BaseURL: func(output.Device) (string, error) {
		return "http://192.0.2.10:8080", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	device := output.Device{
		ID: "renderer", Address: "192.0.2.1", Online: true,
		ProtocolInfo: []string{"http-get:*:audio/x-flac:DLNA.ORG_PN=FLAC;DLNA.ORG_OP=01"},
	}
	return service, fixture, device, track
}

func TestHandler_grant_is_source_bound_range_capable_and_valid_until_revoke(t *testing.T) {
	content := []byte("0123456789")
	service, fixture, device, track := newOriginalFixture(t, content)
	resource, err := service.Prepare(context.Background(), device, track, "play-one")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Mime != "audio/x-flac" || !resource.Seekable || resource.Size != int64(len(content)) || resource.ArtworkURL != "" {
		t.Fatalf("resource = %+v", resource)
	}
	if strings.Contains(resource.URL, track.ID) || strings.Contains(resource.URL, fixture.path) {
		t.Fatalf("media URL exposes source identity")
	}
	parsed, err := url.Parse(resource.URL)
	if err != nil {
		t.Fatal(err)
	}
	absentArtwork := httptest.NewRequest(http.MethodGet, parsed.RequestURI()+"/artwork", nil)
	absentArtwork.RemoteAddr = "192.0.2.1:4999"
	absentArtworkResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(absentArtworkResponse, absentArtwork)
	if absentArtworkResponse.Code != http.StatusNotFound {
		t.Fatalf("absent artwork status = %d", absentArtworkResponse.Code)
	}

	wrongSource := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	wrongSource.RemoteAddr = "192.0.2.2:5000"
	wrongResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(wrongResponse, wrongSource)
	if wrongResponse.Code != http.StatusForbidden || fixture.openCount() != 1 {
		t.Fatalf("wrong-source response = %d, opens = %d", wrongResponse.Code, fixture.openCount())
	}

	head := httptest.NewRequest(http.MethodHead, parsed.RequestURI(), nil)
	head.RemoteAddr = "192.0.2.1:5001"
	headResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 || headResponse.Header().Get("Content-Length") != "10" || headResponse.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("HEAD response = %d headers=%v body=%q", headResponse.Code, headResponse.Header(), headResponse.Body.String())
	}

	for _, value := range []struct {
		header     string
		body       string
		length     string
		rangeValue string
	}{
		{header: "bytes=2-5", body: "2345", length: "4", rangeValue: "bytes 2-5/10"},
		{header: "bytes=-2", body: "89", length: "2", rangeValue: "bytes 8-9/10"},
	} {
		request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
		request.RemoteAddr = "192.0.2.1:5002"
		request.Header.Set("Range", value.header)
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusPartialContent || response.Body.String() != value.body ||
			response.Header().Get("Content-Range") != value.rangeValue || response.Header().Get("Content-Length") != value.length {
			t.Fatalf("range %q response = %d headers=%v body=%q", value.header, response.Code, response.Header(), response.Body.String())
		}
	}

	service.Revoke("play-one")
	revoked := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	revoked.RemoteAddr = "192.0.2.1:5003"
	revokedResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(revokedResponse, revoked)
	if revokedResponse.Code != http.StatusNotFound || fixture.openCount() != 4 {
		t.Fatalf("revoked response = %d, opens = %d", revokedResponse.Code, fixture.openCount())
	}
}

func TestHandler_artwork_grant_is_track_source_and_revocation_bound(t *testing.T) {
	service, fixture, device, track := newOriginalFixture(t, []byte("audio"))
	artwork := []byte("\xff\xd8cached-jpeg\xff\xd9")
	artworkPath := filepath.Join(filepath.Dir(fixture.path), "cover.jpg")
	if err := os.WriteFile(artworkPath, artwork, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.setArtwork("cover-one", artworkPath, "image/jpeg")

	resource, err := service.Prepare(context.Background(), device, track, "artwork-play")
	if err != nil {
		t.Fatal(err)
	}
	if resource.ArtworkURL == "" || strings.Contains(resource.ArtworkURL, "cover-one") || strings.Contains(resource.ArtworkURL, artworkPath) {
		t.Fatalf("artwork URL exposes no usable opaque grant: %q", resource.ArtworkURL)
	}
	parsed, err := url.Parse(resource.ArtworkURL)
	if err != nil {
		t.Fatal(err)
	}

	wrongSource := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	wrongSource.RemoteAddr = "192.0.2.2:5100"
	wrongSourceResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(wrongSourceResponse, wrongSource)
	if wrongSourceResponse.Code != http.StatusForbidden {
		t.Fatalf("wrong-source artwork status = %d", wrongSourceResponse.Code)
	}

	head := httptest.NewRequest(http.MethodHead, parsed.RequestURI(), nil)
	head.RemoteAddr = "192.0.2.1:5101"
	headResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 ||
		headResponse.Header().Get("Content-Type") != "image/jpeg" ||
		headResponse.Header().Get("Content-Length") != "15" {
		t.Fatalf("artwork HEAD = %d headers=%v body=%q", headResponse.Code, headResponse.Header(), headResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	get.RemoteAddr = "192.0.2.1:5102"
	getResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Header().Get("Content-Type") != "image/jpeg" ||
		getResponse.Header().Get("Cache-Control") != "private, no-store" ||
		getResponse.Body.String() != string(artwork) {
		t.Fatalf("artwork GET = %d headers=%v body=%q", getResponse.Code, getResponse.Header(), getResponse.Body.String())
	}

	fixture.setArtwork("cover-two", artworkPath, "image/jpeg")
	staleResponse := httptest.NewRecorder()
	stale := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	stale.RemoteAddr = "192.0.2.1:5103"
	service.Handler().ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusNotFound {
		t.Fatalf("stale artwork status = %d", staleResponse.Code)
	}

	fixture.setArtwork("cover-one", artworkPath, "image/jpeg")
	originalInfo, err := os.Stat(artworkPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(artworkPath, artworkPath+".removed"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artworkPath, []byte("\xff\xd8other-cover\xff\xd9"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(artworkPath, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	replaced := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	replaced.RemoteAddr = "192.0.2.1:5104"
	replacedResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(replacedResponse, replaced)
	if replacedResponse.Code != http.StatusNotFound {
		t.Fatalf("replaced artwork status = %d", replacedResponse.Code)
	}

	replacementResource, err := service.Prepare(context.Background(), device, track, "artwork-play")
	if err != nil {
		t.Fatal(err)
	}
	if replacementResource.ArtworkURL == "" {
		t.Fatal("replacement resource omitted available artwork")
	}
	oldGrant := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	oldGrant.RemoteAddr = "192.0.2.1:5105"
	oldGrantResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(oldGrantResponse, oldGrant)
	if oldGrantResponse.Code != http.StatusNotFound {
		t.Fatalf("replaced artwork grant status = %d", oldGrantResponse.Code)
	}

	service.Revoke("artwork-play")
	revokedURL, _ := url.Parse(replacementResource.ArtworkURL)
	revoked := httptest.NewRequest(http.MethodGet, revokedURL.RequestURI(), nil)
	revoked.RemoteAddr = "192.0.2.1:5106"
	revokedResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(revokedResponse, revoked)
	if revokedResponse.Code != http.StatusNotFound {
		t.Fatalf("revoked artwork status = %d", revokedResponse.Code)
	}
}

func TestHandler_rejects_same_metadata_file_replacement_after_prepare(t *testing.T) {
	service, fixture, device, track := newOriginalFixture(t, []byte("original"))
	resource, err := service.Prepare(context.Background(), device, track, "replacement-play")
	if err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	removed := fixture.path + ".removed"
	if err := os.Rename(fixture.path, removed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.path, []byte("replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fixture.path, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(resource.URL)
	request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	request.RemoteAddr = "192.0.2.1:5050"
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), fixture.path) {
		t.Fatalf("replacement response = %d %q", response.Code, response.Body.String())
	}
}

func TestPrepare_replaces_same_play_binding_and_prefers_original_over_FFmpeg(t *testing.T) {
	service, fixture, device, track := newOriginalFixture(t, []byte("original"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service, err = newService(fixture, Config{
		BaseURL:    func(output.Device) (string, error) { return "http://192.0.2.10:8080", nil },
		FFmpegPath: executable,
		Transcode:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	device.ProtocolInfo = []string{"http-get:*:audio/L16:DLNA.ORG_PN=LPCM", "http-get:*:audio/flac:*"}
	first, err := service.Prepare(context.Background(), device, track, "same-play")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Prepare(context.Background(), device, track, "same-play")
	if err != nil {
		t.Fatal(err)
	}
	if first.URL == second.URL || !second.Seekable || second.Mime != "audio/flac" {
		t.Fatalf("replacement resources = first %+v, second %+v", first, second)
	}
	firstURL, _ := url.Parse(first.URL)
	oldRequest := httptest.NewRequest(http.MethodGet, firstURL.RequestURI(), nil)
	oldRequest.RemoteAddr = "192.0.2.1:6000"
	oldResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(oldResponse, oldRequest)
	if oldResponse.Code != http.StatusNotFound {
		t.Fatalf("obsolete binding status = %d", oldResponse.Code)
	}
	secondURL, _ := url.Parse(second.URL)
	request := httptest.NewRequest(http.MethodGet, secondURL.RequestURI(), nil)
	request.RemoteAddr = "192.0.2.1:6001"
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "original" {
		t.Fatalf("original response = %d %q", response.Code, response.Body.String())
	}
}

func TestPrepare_marks_FFmpeg_fallback_nonseekable_without_starting_it_for_HEAD(t *testing.T) {
	service, fixture, device, track := newOriginalFixture(t, []byte("source"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service, err = newService(fixture, Config{
		BaseURL:    func(output.Device) (string, error) { return "http://192.0.2.10:8080", nil },
		FFmpegPath: executable,
		Transcode:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	device.ProtocolInfo = []string{"http-get:*:audio/L16:DLNA.ORG_PN=LPCM"}
	resource, err := service.Prepare(context.Background(), device, track, "transformed-play")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Seekable || resource.Size != 0 || resource.Mime != l16Mime {
		t.Fatalf("transformed resource = %+v", resource)
	}
	parsed, _ := url.Parse(resource.URL)
	head := httptest.NewRequest(http.MethodHead, parsed.RequestURI(), nil)
	head.RemoteAddr = "192.0.2.1:7000"
	headResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusOK || headResponse.Header().Get("Content-Length") != "" {
		t.Fatalf("transformed HEAD = %d headers=%v", headResponse.Code, headResponse.Header())
	}
	ranged := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	ranged.RemoteAddr = "192.0.2.1:7001"
	ranged.Header.Set("Range", "bytes=0-0")
	rangeResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(rangeResponse, ranged)
	if rangeResponse.Code != http.StatusRequestedRangeNotSatisfiable || rangeResponse.Header().Get("Accept-Ranges") != "" {
		t.Fatalf("transformed range = %d headers=%v", rangeResponse.Code, rangeResponse.Header())
	}
	t.Setenv(ffmpegHelperEnvironment, "copy")
	get := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	get.RemoteAddr = "192.0.2.1:7002"
	getResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.String() != "source" || getResponse.Header().Get("Content-Length") != "" {
		t.Fatalf("transformed GET = %d headers=%v body=%q", getResponse.Code, getResponse.Header(), getResponse.Body.String())
	}
}

type deadlineConnection struct {
	mu       sync.Mutex
	deadline time.Time
	changed  chan struct{}
}

func newDeadlineConnection() *deadlineConnection {
	return &deadlineConnection{changed: make(chan struct{})}
}

func (connection *deadlineConnection) setWriteDeadline(deadline time.Time) error {
	connection.mu.Lock()
	connection.deadline = deadline
	close(connection.changed)
	connection.changed = make(chan struct{})
	connection.mu.Unlock()
	return nil
}

func (connection *deadlineConnection) snapshot() (time.Time, <-chan struct{}) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.deadline, connection.changed
}

type deadlineResponseWriter struct {
	connection *deadlineConnection
	header     http.Header
	started    chan struct{}
	once       sync.Once
	body       strings.Builder
	status     int
	block      bool
}

func (writer *deadlineResponseWriter) Header() http.Header    { return writer.header }
func (writer *deadlineResponseWriter) WriteHeader(status int) { writer.status = status }
func (writer *deadlineResponseWriter) SetWriteDeadline(deadline time.Time) error {
	return writer.connection.setWriteDeadline(deadline)
}
func (writer *deadlineResponseWriter) Write(value []byte) (int, error) {
	writer.once.Do(func() {
		if writer.started != nil {
			close(writer.started)
		}
	})
	if writer.block {
		for {
			deadline, changed := writer.connection.snapshot()
			if !deadline.IsZero() && !deadline.After(time.Now()) {
				return 0, os.ErrDeadlineExceeded
			}
			<-changed
		}
	}
	deadline, _ := writer.connection.snapshot()
	if !deadline.IsZero() && !deadline.After(time.Now()) {
		return 0, os.ErrDeadlineExceeded
	}
	return writer.body.Write(value)
}

func TestHandler_revoke_interrupts_blocked_artwork_write(t *testing.T) {
	service, fixture, device, track := newOriginalFixture(t, []byte("audio"))
	artworkPath := filepath.Join(filepath.Dir(fixture.path), "large-cover.jpg")
	if err := os.WriteFile(artworkPath, make([]byte, 256*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.setArtwork("large-cover", artworkPath, "image/jpeg")
	resource, err := service.Prepare(context.Background(), device, track, "active-artwork-play")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(resource.ArtworkURL)
	request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	request.RemoteAddr = "192.0.2.1:7900"
	connection := newDeadlineConnection()
	writer := &deadlineResponseWriter{
		connection: connection,
		header:     make(http.Header),
		started:    make(chan struct{}),
		block:      true,
	}
	done := make(chan struct{})
	go func() {
		service.Handler().ServeHTTP(writer, request)
		close(done)
	}()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("artwork stream did not begin")
	}
	service.Revoke("active-artwork-play")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("revoked artwork stream remained blocked")
	}
	if writer.status != http.StatusOK || writer.body.Len() != 0 {
		t.Fatalf("revoked artwork response status=%d bytes=%d", writer.status, writer.body.Len())
	}
	deadline, _ := connection.snapshot()
	if !deadline.IsZero() {
		t.Fatalf("revoked artwork left write deadline %v on connection", deadline)
	}
}

func TestHandler_cancellation_interrupts_blocked_original_and_transformed_writes(t *testing.T) {
	for _, transformed := range []bool{false, true} {
		for _, cancellation := range []string{"revoke", "replacement", "request"} {
			name := cancellation + "-original"
			if transformed {
				name = cancellation + "-transformed"
			}
			t.Run(name, func(t *testing.T) {
				content := make([]byte, 256*1024)
				service, fixture, device, track := newOriginalFixture(t, content)
				if transformed {
					executable, err := os.Executable()
					if err != nil {
						t.Fatal(err)
					}
					service, err = newService(fixture, Config{
						BaseURL:    func(output.Device) (string, error) { return "http://192.0.2.10:8080", nil },
						FFmpegPath: executable,
						Transcode:  true,
					})
					if err != nil {
						t.Fatal(err)
					}
					device.ProtocolInfo = []string{"http-get:*:audio/L16:DLNA.ORG_PN=LPCM"}
					t.Setenv(ffmpegHelperEnvironment, "wait")
				}
				resource, err := service.Prepare(context.Background(), device, track, "active-play")
				if err != nil {
					t.Fatal(err)
				}
				parsed, _ := url.Parse(resource.URL)
				requestContext, cancelRequest := context.WithCancel(context.Background())
				defer cancelRequest()
				request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil).WithContext(requestContext)
				request.RemoteAddr = "192.0.2.1:8000"
				connection := newDeadlineConnection()
				writer := &deadlineResponseWriter{
					connection: connection,
					header:     make(http.Header),
					started:    make(chan struct{}),
					block:      true,
				}
				done := make(chan struct{})
				go func() {
					service.Handler().ServeHTTP(writer, request)
					close(done)
				}()
				select {
				case <-writer.started:
				case <-time.After(time.Second):
					t.Fatal("stream did not begin")
				}
				select {
				case <-done:
					t.Fatal("stream ended before cancellation")
				case <-time.After(25 * time.Millisecond):
				}

				followOn := resource
				switch cancellation {
				case "revoke":
					service.Revoke("active-play")
					followOn, err = service.Prepare(context.Background(), device, track, "active-play")
				case "replacement":
					followOn, err = service.Prepare(context.Background(), device, track, "active-play")
				case "request":
					cancelRequest()
				}
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("cancelled stream remained blocked in response write")
				}
				if writer.status != http.StatusOK || writer.body.Len() >= len(content) {
					t.Fatalf("cancelled stream status=%d bytes=%d total=%d", writer.status, writer.body.Len(), len(content))
				}
				deadline, _ := connection.snapshot()
				if !deadline.IsZero() {
					t.Fatalf("cancelled request left write deadline %v on connection", deadline)
				}

				if transformed {
					t.Setenv(ffmpegHelperEnvironment, "copy")
				}
				followOnURL, _ := url.Parse(followOn.URL)
				followOnRequest := httptest.NewRequest(http.MethodGet, followOnURL.RequestURI(), nil)
				followOnRequest.RemoteAddr = "192.0.2.1:8001"
				followOnWriter := &deadlineResponseWriter{
					connection: connection,
					header:     make(http.Header),
				}
				service.Handler().ServeHTTP(followOnWriter, followOnRequest)
				if followOnWriter.status != http.StatusOK || followOnWriter.body.String() != string(content) {
					t.Fatalf("follow-on response status=%d bytes=%d", followOnWriter.status, followOnWriter.body.Len())
				}
			})
		}
	}
}

func TestTranscoder_context_cancellation_terminates_the_process(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.flac")
	if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	t.Setenv(ffmpegHelperEnvironment, "wait")
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := newTranscoder(executable).open(ctx, source, transcodeL16)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan struct{})
	go func() {
		_ = stream.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("transcode process survived cancellation")
	}
}

func TestSelectRepresentation_honors_MIME_aliases_and_DLNA_profiles(t *testing.T) {
	track := library.Track{Format: "mp3", Mime: "audio/mpeg"}
	selected, err := selectRepresentation(track, library.AudioProperties{}, output.ProtocolUPnP, []string{"http-get:*:audio/x-mp3:DLNA.ORG_PN=MP3"}, false)
	if err != nil || selected.mime != "audio/x-mp3" || selected.transformed {
		t.Fatalf("alias selection = %+v, %v", selected, err)
	}
	if _, err := selectRepresentation(track, library.AudioProperties{}, output.ProtocolUPnP, []string{"http-get:*:audio/mpeg:DLNA.ORG_PN=AAC_ISO"}, false); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("mismatched profile error = %v", err)
	}
	if _, err := selectRepresentation(track, library.AudioProperties{}, output.ProtocolUPnP, []string{"rtsp-rtp-udp:*:audio/mpeg:*"}, false); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("non-HTTP sink error = %v", err)
	}
	if _, err := selectRepresentation(track, library.AudioProperties{}, output.ProtocolUPnP, []string{"http-get:*:audio/L16;rate=48000;channels=2:*"}, true); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("incompatible L16 parameters error = %v", err)
	}
	m4a := library.Track{Format: "m4a", Mime: "audio/mp4"}
	if _, err := selectRepresentation(m4a, library.AudioProperties{}, output.ProtocolUPnP, []string{"http-get:*:audio/mp4:DLNA.ORG_PN=AAC_ISO"}, false); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("unverified M4A codec profile error = %v", err)
	}
}

func TestSelectRepresentation_CastUsesDocumentedNativeFormatsAndConservativeBounds(t *testing.T) {
	tests := []struct {
		name      string
		track     library.Track
		audio     library.AudioProperties
		mime      string
		supported bool
	}{
		{
			name: "FLAC 96 kHz 24-bit", track: library.Track{Format: "flac", Mime: "audio/x-flac"},
			audio: library.AudioProperties{Codec: "FLAC", SampleRate: pointer[int64](96_000), Channels: pointer(2), BitsPerSample: pointer(24)},
			mime:  "audio/flac", supported: true,
		},
		{
			name: "FLAC above 96 kHz", track: library.Track{Format: "flac", Mime: "audio/flac"},
			audio: library.AudioProperties{Codec: "FLAC", SampleRate: pointer[int64](192_000), Channels: pointer(2), BitsPerSample: pointer(24)},
		},
		{
			name: "FLAC above 24-bit", track: library.Track{Format: "flac", Mime: "audio/flac"},
			audio: library.AudioProperties{Codec: "FLAC", SampleRate: pointer[int64](96_000), Channels: pointer(2), BitsPerSample: pointer(32)},
		},
		{
			name: "FLAC metadata unavailable", track: library.Track{Format: "flac", Mime: "audio/flac"},
			audio: library.AudioProperties{},
		},
		{
			name: "MP3", track: library.Track{Format: "mp3", Mime: "audio/mpeg"},
			audio: library.AudioProperties{Codec: "MP3", SampleRate: pointer[int64](48_000), Channels: pointer(2)},
			mime:  "audio/mpeg", supported: true,
		},
		{
			name: "MP3 above 48 kHz", track: library.Track{Format: "mp3", Mime: "audio/mpeg"},
			audio: library.AudioProperties{Codec: "MP3", SampleRate: pointer[int64](96_000), Channels: pointer(2)},
		},
		{
			name: "WAV LPCM", track: library.Track{Format: "wav", Mime: "audio/wav"},
			audio: library.AudioProperties{Codec: "PCM", SampleRate: pointer[int64](48_000), Channels: pointer(2), BitsPerSample: pointer(16)},
			mime:  wavMime, supported: true,
		},
		{
			name: "WAV high resolution is not assumed", track: library.Track{Format: "wav", Mime: "audio/wav"},
			audio: library.AudioProperties{Codec: "PCM", SampleRate: pointer[int64](96_000), Channels: pointer(2), BitsPerSample: pointer(24)},
		},
		{
			name: "Ogg Vorbis", track: library.Track{Format: "ogg", Mime: "audio/ogg"},
			audio: library.AudioProperties{Codec: "Vorbis", SampleRate: pointer[int64](48_000), Channels: pointer(2)},
			mime:  "audio/ogg; codecs=vorbis", supported: true,
		},
		{
			name: "Ogg Opus", track: library.Track{Format: "opus", Mime: "audio/ogg"},
			audio: library.AudioProperties{Codec: "Opus", SampleRate: pointer[int64](48_000), Channels: pointer(2)},
			mime:  "audio/ogg; codecs=opus", supported: true,
		},
		{
			name: "M4A AAC", track: library.Track{Format: "m4a", Mime: "audio/mp4"},
			audio: library.AudioProperties{Codec: "AAC", SampleRate: pointer[int64](48_000), Channels: pointer(2)},
			mime:  "audio/mp4", supported: true,
		},
		{
			name: "M4A ALAC is not documented", track: library.Track{Format: "m4a", Mime: "audio/mp4"},
			audio: library.AudioProperties{Codec: "ALAC", SampleRate: pointer[int64](48_000), Channels: pointer(2), BitsPerSample: pointer(16)},
		},
		{
			name: "multichannel is not assumed", track: library.Track{Format: "flac", Mime: "audio/flac"},
			audio: library.AudioProperties{Codec: "FLAC", SampleRate: pointer[int64](48_000), Channels: pointer(6), BitsPerSample: pointer(16)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected, err := selectRepresentation(test.track, test.audio, output.ProtocolCast, []string{"http-get:*:*:*"}, false)
			if test.supported {
				if err != nil || selected.mime != test.mime || selected.transformed {
					t.Fatalf("selection = %+v, %v", selected, err)
				}
				return
			}
			if !errors.Is(err, ErrUnsupportedMedia) {
				t.Fatalf("unsupported selection = %+v, %v", selected, err)
			}
		})
	}
}

func TestPrepare_CastFallbackIsWAVAndUPnPSelectionIsUnchanged(t *testing.T) {
	service, fixture, device, track := newOriginalFixture(t, []byte("source"))
	device.Protocol = output.ProtocolCast
	device.ProtocolInfo = []string{"http-get:*:audio/L16:DLNA.ORG_PN=LPCM"}
	fixture.audio = library.AudioProperties{
		Codec: "FLAC", SampleRate: pointer[int64](192_000), Channels: pointer(2), BitsPerSample: pointer(24),
	}
	_, err := service.Prepare(context.Background(), device, track, "cast-rejected")
	if !errors.Is(err, ErrUnsupportedMedia) || !strings.Contains(err.Error(), "96 kHz") {
		t.Fatalf("native Cast rejection = %v", err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	transcoding, err := newService(fixture, Config{
		BaseURL:    func(output.Device) (string, error) { return "http://192.0.2.10:8080", nil },
		FFmpegPath: executable,
		Transcode:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := transcoding.Prepare(context.Background(), device, track, "cast-fallback")
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Mime != wavMime || fallback.Seekable || fallback.Size != 0 {
		t.Fatalf("Cast fallback = %+v", fallback)
	}
	defer transcoding.Revoke("cast-fallback")
	t.Setenv(ffmpegHelperEnvironment, "copy")
	for _, test := range []struct {
		rangeHeader string
		status      int
	}{
		{"bytes=0-", http.StatusOK},
		{"bytes=4-", http.StatusRequestedRangeNotSatisfiable},
	} {
		request := httptest.NewRequest(http.MethodGet, fallback.URL, nil)
		request.RemoteAddr = "192.0.2.1:7002"
		request.Header.Set("Origin", "https://www.gstatic.com")
		request.Header.Set("Range", test.rangeHeader)
		response := httptest.NewRecorder()
		transcoding.Handler().ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("Cast fallback range %q: status=%d, want %d", test.rangeHeader, response.Code, test.status)
		}
	}

	device.Protocol = output.ProtocolUPnP
	device.ProtocolInfo = []string{"http-get:*:audio/x-flac:DLNA.ORG_PN=FLAC"}
	original, err := service.Prepare(context.Background(), device, track, "upnp-original")
	if err != nil {
		t.Fatal(err)
	}
	if original.Mime != "audio/x-flac" || !original.Seekable || original.Size != int64(len("source")) {
		t.Fatalf("UPnP original = %+v", original)
	}
}

func TestHandler_CastGrantSupportsRangesArtworkAndBoundCORS(t *testing.T) {
	content := []byte("0123456789")
	service, fixture, device, track := newOriginalFixture(t, content)
	artwork := []byte("\xff\xd8cast-cover\xff\xd9")
	artworkPath := filepath.Join(filepath.Dir(fixture.path), "cast-cover.jpg")
	if err := os.WriteFile(artworkPath, artwork, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.setArtwork("cast-cover", artworkPath, "image/jpeg")
	device.Protocol = output.ProtocolCast
	device.ProtocolInfo = []string{"http-get:*:audio/L16:DLNA.ORG_PN=LPCM"}
	resource, err := service.Prepare(context.Background(), device, track, "cast-play")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Mime != "audio/flac" || resource.ArtworkURL == "" {
		t.Fatalf("Cast resource = %+v", resource)
	}
	mediaURL, _ := url.Parse(resource.URL)
	origin := "https://www.gstatic.com"

	ranged := httptest.NewRequest(http.MethodGet, mediaURL.RequestURI(), nil)
	ranged.RemoteAddr = "192.0.2.1:8100"
	ranged.Header.Set("Origin", origin)
	ranged.Header.Set("Range", "bytes=2-5")
	rangedResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(rangedResponse, ranged)
	if rangedResponse.Code != http.StatusPartialContent || rangedResponse.Body.String() != "2345" ||
		rangedResponse.Header().Get("Content-Type") != "audio/flac" ||
		rangedResponse.Header().Get("Content-Range") != "bytes 2-5/10" ||
		rangedResponse.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("Cast range = %d headers=%v body=%q", rangedResponse.Code, rangedResponse.Header(), rangedResponse.Body.String())
	}

	artworkURL, _ := url.Parse(resource.ArtworkURL)
	artworkRequest := httptest.NewRequest(http.MethodGet, artworkURL.RequestURI(), nil)
	artworkRequest.RemoteAddr = "192.0.2.1:8101"
	artworkRequest.Header.Set("Origin", origin)
	artworkResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(artworkResponse, artworkRequest)
	if artworkResponse.Code != http.StatusOK || artworkResponse.Body.String() != string(artwork) ||
		artworkResponse.Header().Get("Content-Type") != "image/jpeg" ||
		artworkResponse.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("Cast artwork = %d headers=%v body=%q", artworkResponse.Code, artworkResponse.Header(), artworkResponse.Body.String())
	}

	preflight := httptest.NewRequest(http.MethodOptions, mediaURL.RequestURI(), nil)
	preflight.RemoteAddr = "192.0.2.1:8102"
	preflight.Header.Set("Origin", origin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "Range, Content-Type, Accept-Encoding")
	preflightResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent ||
		preflightResponse.Header().Get("Access-Control-Allow-Origin") != origin ||
		preflightResponse.Header().Get("Access-Control-Allow-Methods") != "GET, HEAD" ||
		preflightResponse.Header().Get("Access-Control-Allow-Headers") != "Content-Type, Accept-Encoding, Range" {
		t.Fatalf("Cast preflight = %d headers=%v", preflightResponse.Code, preflightResponse.Header())
	}

	badHeader := httptest.NewRequest(http.MethodOptions, mediaURL.RequestURI(), nil)
	badHeader.RemoteAddr = "192.0.2.1:8103"
	badHeader.Header.Set("Origin", origin)
	badHeader.Header.Set("Access-Control-Request-Method", http.MethodGet)
	badHeader.Header.Set("Access-Control-Request-Headers", "Authorization")
	badHeaderResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(badHeaderResponse, badHeader)
	if badHeaderResponse.Code != http.StatusForbidden {
		t.Fatalf("disallowed Cast preflight = %d", badHeaderResponse.Code)
	}

	post := httptest.NewRequest(http.MethodPost, mediaURL.RequestURI(), strings.NewReader("{}"))
	post.RemoteAddr = "192.0.2.1:8104"
	post.Header.Set("Origin", origin)
	postResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusMethodNotAllowed || postResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("Cast POST = %d headers=%v", postResponse.Code, postResponse.Header())
	}

	multipleOrigins := httptest.NewRequest(http.MethodGet, mediaURL.RequestURI(), nil)
	multipleOrigins.RemoteAddr = "192.0.2.1:8104"
	multipleOrigins.Header.Add("Origin", origin)
	multipleOrigins.Header.Add("Origin", "https://example.invalid")
	multipleOriginsResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(multipleOriginsResponse, multipleOrigins)
	if multipleOriginsResponse.Code != http.StatusForbidden {
		t.Fatalf("multiple-origin Cast request = %d", multipleOriginsResponse.Code)
	}

	wrongIP := httptest.NewRequest(http.MethodGet, mediaURL.RequestURI(), nil)
	wrongIP.RemoteAddr = "192.0.2.2:8105"
	wrongIP.Header.Set("Origin", origin)
	wrongIPResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(wrongIPResponse, wrongIP)
	if wrongIPResponse.Code != http.StatusForbidden || wrongIPResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("wrong-IP Cast request = %d headers=%v", wrongIPResponse.Code, wrongIPResponse.Header())
	}

	service.Revoke("cast-play")
	revoked := httptest.NewRequest(http.MethodGet, mediaURL.RequestURI(), nil)
	revoked.RemoteAddr = "192.0.2.1:8106"
	revoked.Header.Set("Origin", origin)
	revokedResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(revokedResponse, revoked)
	if revokedResponse.Code != http.StatusNotFound || revokedResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("revoked Cast request = %d headers=%v", revokedResponse.Code, revokedResponse.Header())
	}
}

func TestHandler_UPnPMediaRetainsSameOriginPolicy(t *testing.T) {
	service, _, device, track := newOriginalFixture(t, []byte("audio"))
	device.Protocol = output.ProtocolUPnP
	resource, err := service.Prepare(context.Background(), device, track, "upnp-origin")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(resource.URL)

	sameOrigin := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	sameOrigin.Host = parsed.Host
	sameOrigin.RemoteAddr = "192.0.2.1:8200"
	sameOrigin.Header.Set("Origin", "http://"+parsed.Host)
	sameOriginResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(sameOriginResponse, sameOrigin)
	if sameOriginResponse.Code != http.StatusOK || sameOriginResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("same-origin UPnP request = %d headers=%v", sameOriginResponse.Code, sameOriginResponse.Header())
	}

	for name, request := range map[string]*http.Request{
		"cross-origin": func() *http.Request {
			value := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
			value.Header.Set("Origin", "https://example.invalid")
			return value
		}(),
		"cross-site-fetch": func() *http.Request {
			value := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
			value.Header.Set("Sec-Fetch-Site", "cross-site")
			return value
		}(),
		"preflight": func() *http.Request {
			value := httptest.NewRequest(http.MethodOptions, parsed.RequestURI(), nil)
			value.Header.Set("Origin", "http://"+parsed.Host)
			value.Header.Set("Access-Control-Request-Method", http.MethodGet)
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			request.Host = parsed.Host
			request.RemoteAddr = "192.0.2.1:8201"
			response := httptest.NewRecorder()
			service.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("UPnP rejection = %d headers=%v", response.Code, response.Header())
			}
		})
	}
}
