package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemRouteRequiresAuthentication(t *testing.T) {
	fixture := startAPI(t, false)
	query := url.Values{"path": {t.TempDir()}}.Encode()
	expectStatus(t, fixture.request(t, http.MethodGet, "/api/v1/filesystem?"+query, "", nil), http.StatusUnauthorized)
}

func TestFilesystemRouteListsRootsAndPaginatesEligibleEntries(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)

	rootPage, _ := requestFilesystemPage(t, fixture, url.Values{})
	if rootPage.Path != "" || rootPage.Parent != nil || rootPage.NextOffset != nil || rootPage.Entries == nil || len(rootPage.Entries) != 0 || len(rootPage.Roots) == 0 {
		t.Fatalf("filesystem roots response=%#v", rootPage)
	}

	directory := t.TempDir()
	expectedDirectories := make(map[string]bool, 101)
	for index := range 101 {
		name := fmt.Sprintf("directory-%03d", index)
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
			t.Fatal(err)
		}
		expectedDirectories[name] = true
	}
	fileName := "regular-audio.flac"
	secret := []byte("filesystem-endpoint-must-not-read-these-bytes")
	if err := os.WriteFile(filepath.Join(directory, fileName), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	symlinks := make(map[string]bool)
	if err := os.Symlink(filepath.Join(directory, "directory-000"), filepath.Join(directory, "linked-directory")); err == nil {
		symlinks["linked-directory"] = true
	}
	if err := os.Symlink(filepath.Join(directory, fileName), filepath.Join(directory, "linked-file")); err == nil {
		symlinks["linked-file"] = true
	}

	directoryEntries := collectFilesystemEntries(t, fixture, directory, "directory", 101)
	if len(directoryEntries) != len(expectedDirectories) {
		t.Fatalf("directory entries=%d, want %d", len(directoryEntries), len(expectedDirectories))
	}
	for _, entry := range directoryEntries {
		if entry.Kind != "directory" || !expectedDirectories[entry.Name] || entry.Path != filepath.Join(directory, entry.Name) {
			t.Fatalf("unexpected directory entry=%#v", entry)
		}
		if symlinks[entry.Name] || entry.Name == fileName {
			t.Fatalf("directory listing exposed an ineligible entry=%#v", entry)
		}
		delete(expectedDirectories, entry.Name)
	}
	if len(expectedDirectories) != 0 {
		t.Fatalf("directory listing omitted entries=%v", expectedDirectories)
	}

	fileEntries, bodies := collectFilesystemEntriesWithBodies(t, fixture, directory, "file", 102)
	if len(fileEntries) != 102 {
		t.Fatalf("file-mode entries=%d, want 102", len(fileEntries))
	}
	regularFiles := 0
	for _, entry := range fileEntries {
		if symlinks[entry.Name] {
			t.Fatalf("file listing exposed a symbolic link=%#v", entry)
		}
		switch entry.Kind {
		case "directory":
			if entry.Name == fileName {
				t.Fatalf("regular file classified as directory=%#v", entry)
			}
		case "file":
			if entry.Name != fileName {
				t.Fatalf("unexpected regular file=%#v", entry)
			}
			regularFiles++
		default:
			t.Fatalf("unsupported entry kind=%#v", entry)
		}
	}
	if regularFiles != 1 {
		t.Fatalf("regular file count=%d, want 1", regularFiles)
	}
	for _, body := range bodies {
		if bytes.Contains(body, secret) {
			t.Fatal("filesystem response exposed file contents")
		}
	}
}

func TestFilesystemRouteRejectsInvalidAndUnavailablePathsSafely(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	directory := t.TempDir()
	file := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(file, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(directory, "missing-directory")

	tests := []struct {
		name   string
		query  url.Values
		status int
		code   string
	}{
		{name: "relative path", query: url.Values{"path": {"relative"}}, status: http.StatusBadRequest, code: "INVALID_PATH"},
		{name: "regular file", query: url.Values{"path": {file}}, status: http.StatusBadRequest, code: "PATH_NOT_DIRECTORY"},
		{name: "missing directory", query: url.Values{"path": {missing}}, status: http.StatusNotFound, code: "PATH_NOT_FOUND"},
		{name: "invalid kind", query: url.Values{"path": {directory}, "kind": {"content"}}, status: http.StatusBadRequest, code: "INVALID_QUERY"},
		{name: "negative offset", query: url.Values{"path": {directory}, "offset": {"-1"}}, status: http.StatusBadRequest, code: "INVALID_QUERY"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.request(t, http.MethodGet, "/api/v1/filesystem?"+test.query.Encode(), "", nil)
			defer response.Body.Close()
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status || body.Error.Code != test.code {
				t.Fatalf("status=%d code=%q, want status=%d code=%q", response.StatusCode, body.Error.Code, test.status, test.code)
			}
			if strings.Contains(body.Error.Message, directory) || strings.Contains(body.Error.Message, file) || strings.Contains(body.Error.Message, missing) {
				t.Fatal("filesystem error exposed an absolute path")
			}
		})
	}
}

func requestFilesystemPage(t *testing.T, fixture apiFixture, query url.Values) (filesystemPage, []byte) {
	t.Helper()
	response := fixture.request(t, http.MethodGet, "/api/v1/filesystem?"+query.Encode(), "", nil)
	defer response.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("filesystem status=%d body=%s", response.StatusCode, raw)
	}
	var page filesystemPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	return page, raw
}

func collectFilesystemEntries(t *testing.T, fixture apiFixture, path, kind string, total int) []filesystemEntry {
	t.Helper()
	entries, _ := collectFilesystemEntriesWithBodies(t, fixture, path, kind, total)
	return entries
}

func collectFilesystemEntriesWithBodies(t *testing.T, fixture apiFixture, path, kind string, total int) ([]filesystemEntry, [][]byte) {
	t.Helper()
	first, firstBody := requestFilesystemPage(t, fixture, url.Values{"path": {path}, "kind": {kind}})
	if first.Path != filepath.Clean(path) || first.Parent == nil || *first.Parent != filepath.Dir(filepath.Clean(path)) || len(first.Entries) != filesystemPageSize || first.NextOffset == nil || *first.NextOffset != filesystemPageSize {
		t.Fatalf("first filesystem page=%#v", first)
	}
	second, secondBody := requestFilesystemPage(t, fixture, url.Values{"path": {path}, "kind": {kind}, "offset": {fmt.Sprint(*first.NextOffset)}})
	if second.NextOffset != nil || len(second.Entries) != total-filesystemPageSize {
		t.Fatalf("second filesystem page=%#v", second)
	}
	entries := append(first.Entries, second.Entries...)
	return entries, [][]byte{firstBody, secondBody}
}
