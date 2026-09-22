package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/downloads"
	"github.com/jastreamer/jastreamer-server/internal/library"
)

func TestDownloadRoutesRequireAuthServeVerifiedRangesAndLeaveQueueUntouched(t *testing.T) {
	fixture := startAPI(t, false)
	expectStatus(t, fixture.request(t, http.MethodGet, "/api/v1/downloads/capabilities", "", nil), http.StatusUnauthorized)
	fixture.setup(t)

	root := t.TempDir()
	sourcePath := filepath.Join(root, "song.wav")
	writeHTTPDownloadWAV(t, sourcePath, 8_000)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.catalog.SetRoots([]library.Root{{ID: "music", Name: "Music", Path: root}}); err != nil {
		t.Fatal(err)
	}
	scan, err := fixture.catalog.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	waitHTTPDownloadScan(t, fixture.catalog, scan.ID)
	page, err := fixture.catalog.Browse(t.Context(), library.Query{Kind: "tracks", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	track := page.Items.([]library.Track)[0]

	beforeResponse := fixture.request(t, http.MethodGet, "/api/v1/queue", "", nil)
	beforeQueue, err := io.ReadAll(beforeResponse.Body)
	beforeResponse.Body.Close()
	if err != nil || beforeResponse.StatusCode != http.StatusOK {
		t.Fatalf("queue before download status=%d error=%v", beforeResponse.StatusCode, err)
	}

	// A null path must not be interpreted as an explicit request for the entire root.
	expectStatus(t, fixture.request(t, http.MethodPost, "/api/v1/downloads", `{"kind":"folder","root_id":"music","path":null,"quality":"original"}`, nil), http.StatusBadRequest)

	response := fixture.request(t, http.MethodPost, "/api/v1/downloads", `{"kind":"track","id":"`+track.ID+`","quality":"original"}`, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create status=%d body=%s", response.StatusCode, body)
	}
	var manifest downloads.Manifest
	if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for manifest.Status == "preparing" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		poll := fixture.request(t, http.MethodGet, "/api/v1/downloads/"+manifest.ID, "", nil)
		if poll.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(poll.Body)
			poll.Body.Close()
			t.Fatalf("poll status=%d body=%s", poll.StatusCode, body)
		}
		if err := json.NewDecoder(poll.Body).Decode(&manifest); err != nil {
			poll.Body.Close()
			t.Fatal(err)
		}
		poll.Body.Close()
	}
	if manifest.Status != "ready" || len(manifest.Tracks) != 1 {
		body, _ := json.Marshal(manifest)
		t.Fatalf("manifest = %s", body)
	}
	digest := sha256.Sum256(source)
	if manifest.Tracks[0].ByteSize != int64(len(source)) || manifest.Tracks[0].SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("ready track = %+v", manifest.Tracks[0])
	}

	filePath := manifest.Tracks[0].MediaPath
	head := fixture.request(t, http.MethodHead, filePath, "", nil)
	if head.StatusCode != http.StatusOK || head.Header.Get("Content-Length") != strconv.Itoa(len(source)) || head.Header.Get("ETag") != strconv.Quote(manifest.Tracks[0].SHA256) {
		head.Body.Close()
		t.Fatalf("HEAD status=%d length=%q etag=%q", head.StatusCode, head.Header.Get("Content-Length"), head.Header.Get("ETag"))
	}
	head.Body.Close()

	partial := fixture.request(t, http.MethodGet, filePath, "", func(request *http.Request) {
		request.Header.Set("Range", "bytes=5-15")
		request.Header.Set("If-Range", strconv.Quote(manifest.Tracks[0].SHA256))
	})
	partialBytes, err := io.ReadAll(partial.Body)
	partial.Body.Close()
	if err != nil || partial.StatusCode != http.StatusPartialContent || !bytes.Equal(partialBytes, source[5:16]) || partial.Header.Get("Content-Range") != "bytes 5-15/"+strconv.Itoa(len(source)) {
		t.Fatalf("range status=%d content-range=%q bytes=%x error=%v", partial.StatusCode, partial.Header.Get("Content-Range"), partialBytes, err)
	}

	mismatch := fixture.request(t, http.MethodGet, filePath, "", func(request *http.Request) {
		request.Header.Set("Range", "bytes=5-15")
		request.Header.Set("If-Range", `"different"`)
	})
	mismatchBytes, err := io.ReadAll(mismatch.Body)
	mismatch.Body.Close()
	if err != nil || mismatch.StatusCode != http.StatusOK || !bytes.Equal(mismatchBytes, source) {
		t.Fatalf("If-Range mismatch status=%d bytes=%d error=%v", mismatch.StatusCode, len(mismatchBytes), err)
	}

	afterResponse := fixture.request(t, http.MethodGet, "/api/v1/queue", "", nil)
	afterQueue, err := io.ReadAll(afterResponse.Body)
	afterResponse.Body.Close()
	if err != nil || afterResponse.StatusCode != http.StatusOK || !bytes.Equal(beforeQueue, afterQueue) {
		t.Fatalf("queue changed during download: before=%s after=%s", beforeQueue, afterQueue)
	}
	current, err := os.ReadFile(sourcePath)
	if err != nil || !bytes.Equal(current, source) {
		t.Fatal("download changed the library source")
	}
}

func waitHTTPDownloadScan(t *testing.T, service *library.Service, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := service.Scans(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.ID == id && job.Status == "complete" {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("scan did not finish")
}

func writeHTTPDownloadWAV(t *testing.T, path string, samples int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 44+samples))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+samples))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(8_000))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(8_000))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(8))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(samples))
	buffer.Write(make([]byte, samples))
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
