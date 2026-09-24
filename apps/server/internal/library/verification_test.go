package library

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/errorhistory"

	_ "modernc.org/sqlite"
)

const verificationHelperEnvironment = "JASTREAMER_LIBRARY_VERIFICATION_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(verificationHelperEnvironment); mode != "" {
		runVerificationHelper(mode, os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

func runVerificationHelper(mode string, arguments []string) {
	joined := strings.Join(arguments, " ")
	switch {
	case strings.Contains(joined, "-protocols"):
		fmt.Fprintln(os.Stdout, "Input: fd")
	case strings.Contains(joined, "-encoders"):
		fmt.Fprintln(os.Stdout, "A..... pcm_s32le")
	case strings.Contains(joined, "-muxers"):
		fmt.Fprintln(os.Stdout, "E s32le")
	default:
		_, _ = io.Copy(io.Discard, os.Stdin)
		switch mode {
		case "valid":
			_, _ = os.Stdout.Write([]byte{0, 0, 0, 0})
		case "stderr-success":
			_, _ = os.Stdout.Write([]byte{0, 0, 0, 0})
			fmt.Fprintln(os.Stderr, "Error while decoding stream")
		case "corrupt":
			fmt.Fprintln(os.Stderr, "Invalid data found when processing input")
			os.Exit(2)
		case "unsupported":
			fmt.Fprintln(os.Stderr, "No decoder found for this codec")
			os.Exit(2)
		case "hold":
			time.Sleep(24 * time.Hour)
		default:
			os.Exit(2)
		}
	}
	os.Exit(0)
}

func TestVerificationIsDeferredPausesAndFullScanRepeatsVerification(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 8_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	history, err := errorhistory.New(t.Context(), service.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(verificationHelperEnvironment, "hold")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, History: history, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}

	first, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first = waitScan(t, service, first.ID)
	if first.Status != "complete" {
		t.Fatalf("metadata scan = %+v", first)
	}
	status := waitVerificationState(t, service, "running")
	if status.Total != 1 || status.Pending != 1 || status.Verified != 0 {
		t.Fatalf("deferred verification = %+v", status)
	}

	playback.Store(true)
	status = waitVerificationState(t, service, "paused")
	if status.Reason != "playback" || status.Pending != 1 {
		t.Fatalf("paused verification = %+v", status)
	}
	t.Setenv(verificationHelperEnvironment, "valid")
	playback.Store(false)
	status = waitVerificationState(t, service, "complete")
	if status.Verified != 1 || status.Failed != 0 || status.Unverified != 0 {
		t.Fatalf("resumed verification = %+v", status)
	}

	t.Setenv(verificationHelperEnvironment, "corrupt")
	second, err := service.StartScanMode(t.Context(), ScanModeFull)
	if err != nil {
		t.Fatal(err)
	}
	second = waitScan(t, service, second.ID)
	if second.Status != "complete" || second.Updated != 1 || second.Added != 0 {
		t.Fatalf("full metadata scan = %+v", second)
	}
	status = waitVerificationRunState(t, service, second.ID, "complete")
	if status.Failed != 1 || status.Verified != 0 {
		t.Fatalf("fresh verification after full scan = %+v", status)
	}
	records := waitIntegrityHistory(t, history, 1)
	if records.Total != 1 || len(records.Items) != 1 {
		t.Fatalf("integrity history = %+v", records)
	}
	event := records.Items[0]
	if event.Outcome != "failed" || event.TrackID == "" || event.TrackTitle != "song" || event.RootName != "Music" || event.RelativePath != "song.wav" || event.Code != "AUDIO_DECODE_CORRUPT" {
		t.Fatalf("integrity event = %+v", event)
	}
}

func TestIncrementalVerificationReusesUnchangedAndChecksChangedNewAndDeletedFiles(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "unchanged.wav"), 4_000)
	writeTestWAV(t, filepath.Join(root, "changed.wav"), 4_000)
	writeTestWAV(t, filepath.Join(root, "deleted.wav"), 4_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	t.Setenv(verificationHelperEnvironment, "valid")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}
	first, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, first.ID); completed.Status != "complete" {
		t.Fatalf("initial scan = %+v", completed)
	}
	if status := waitVerificationRunState(t, service, first.ID, "complete"); status.Total != 3 || status.Verified != 3 {
		t.Fatalf("initial verification = %+v", status)
	}

	writeTestWAV(t, filepath.Join(root, "changed.wav"), 8_000)
	if err := os.Remove(filepath.Join(root, "deleted.wav")); err != nil {
		t.Fatal(err)
	}
	writeTestWAV(t, filepath.Join(root, "new.wav"), 4_000)
	t.Setenv(verificationHelperEnvironment, "corrupt")
	second, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, second.ID); completed.Status != "complete" || completed.Added != 1 || completed.Updated != 1 || completed.Unavailable != 1 {
		t.Fatalf("incremental scan = %+v", completed)
	}
	status := waitVerificationRunState(t, service, second.ID, "complete")
	if status.Total != 3 || status.Pending != 0 || status.Verified != 1 || status.Failed != 2 || status.Unverified != 0 {
		t.Fatalf("incremental verification = %+v", status)
	}
	rows, err := service.db.QueryContext(t.Context(), `SELECT relative_path,status FROM library_verification_items ORDER BY relative_path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	items := make(map[string]string)
	for rows.Next() {
		var path, itemStatus string
		if err := rows.Scan(&path, &itemStatus); err != nil {
			t.Fatal(err)
		}
		items[path] = itemStatus
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items["unchanged.wav"] != "verified" || items["changed.wav"] != "failed" || items["new.wav"] != "failed" {
		t.Fatalf("verification items = %#v", items)
	}
	if _, exists := items["deleted.wav"]; exists {
		t.Fatalf("deleted verification item was retained: %#v", items)
	}
}

func TestIncrementalVerificationRetainsFailureAcrossRestart(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "library.sqlite")
	cachePath := filepath.Join(directory, "artwork")
	rootConfig := []Root{{ID: "music", Name: "Music", Path: root}}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	firstContext, stopFirst := context.WithCancel(context.Background())
	firstDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	firstDB.SetMaxOpenConns(1)
	first, err := New(firstContext, firstDB, rootConfig, cachePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstHistory, err := errorhistory.New(firstContext, firstDB, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(verificationHelperEnvironment, "corrupt")
	if err := first.StartVerification(VerificationOptions{FFmpegPath: executable, History: firstHistory}); err != nil {
		t.Fatal(err)
	}
	initial, err := first.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, first, initial.ID); completed.Status != "complete" {
		t.Fatalf("initial scan = %+v", completed)
	}
	if status := waitVerificationRunState(t, first, initial.ID, "complete"); status.Failed != 1 || status.Verified != 0 {
		t.Fatalf("initial verification = %+v", status)
	}
	if records := waitIntegrityHistory(t, firstHistory, 1); records.Total != 1 {
		t.Fatalf("initial integrity history = %+v", records)
	}
	stopFirst()
	waitContext, cancelWait := context.WithTimeout(t.Context(), 2*time.Second)
	if err := first.WaitVerification(waitContext); err != nil {
		t.Fatal(err)
	}
	cancelWait()
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	secondContext, stopSecond := context.WithCancel(context.Background())
	secondDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	secondDB.SetMaxOpenConns(1)
	second, err := New(secondContext, secondDB, rootConfig, cachePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopSecond()
		wait, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = second.WaitVerification(wait)
		cancel()
		_ = secondDB.Close()
	})
	secondHistory, err := errorhistory.New(secondContext, secondDB, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(verificationHelperEnvironment, "valid")
	if err := second.StartVerification(VerificationOptions{FFmpegPath: executable, History: secondHistory}); err != nil {
		t.Fatal(err)
	}
	incremental, err := second.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, second, incremental.ID); completed.Status != "complete" || completed.Updated != 0 {
		t.Fatalf("restart incremental scan = %+v", completed)
	}
	status := waitVerificationRunState(t, second, incremental.ID, "complete")
	if status.Total != 1 || status.Pending != 0 || status.Verified != 0 || status.Failed != 1 || status.Unverified != 0 {
		t.Fatalf("retained verification after restart = %+v", status)
	}
	records, err := secondHistory.List(t.Context(), errorhistory.ListOptions{Kind: "integrity", Limit: 10})
	if err != nil || records.Total != 1 {
		t.Fatalf("retained integrity history = %+v, %v", records, err)
	}
}

func TestIncrementalVerificationRetainsUnverifiedOutcomeAndHistory(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	history, err := errorhistory.New(t.Context(), service.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(verificationHelperEnvironment, "unsupported")
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, History: history, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}
	first, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, first.ID); completed.Status != "complete" {
		t.Fatalf("initial scan = %+v", completed)
	}
	if status := waitVerificationRunState(t, service, first.ID, "complete"); status.Unverified != 1 || status.Verified != 0 || status.Failed != 0 {
		t.Fatalf("initial verification = %+v", status)
	}
	if records := waitIntegrityHistory(t, history, 1); records.Items[0].Outcome != "unknown" || records.Items[0].Code != "FORMAT_UNSUPPORTED" {
		t.Fatalf("initial integrity history = %+v", records)
	}

	t.Setenv(verificationHelperEnvironment, "valid")
	second, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, second.ID); completed.Status != "complete" || completed.Updated != 0 {
		t.Fatalf("incremental scan = %+v", completed)
	}
	status := waitVerificationRunState(t, service, second.ID, "complete")
	if status.Total != 1 || status.Pending != 0 || status.Verified != 0 || status.Failed != 0 || status.Unverified != 1 {
		t.Fatalf("retained unverified result = %+v", status)
	}
	records, err := history.List(t.Context(), errorhistory.ListOptions{Kind: "integrity", Limit: 10})
	if err != nil || records.Total != 1 {
		t.Fatalf("retained integrity history = %+v, %v", records, err)
	}
}

func TestNewScanSupersedesRunningVerificationAndFencesReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "song.wav")
	writeTestWAV(t, path, 4_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	t.Setenv(verificationHelperEnvironment, "hold")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}
	first, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first = waitScan(t, service, first.ID)
	waitVerificationState(t, service, "running")

	writeTestWAV(t, path, 8_000)
	t.Setenv(verificationHelperEnvironment, "valid")
	second, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second = waitScan(t, service, second.ID)
	if second.Status != "complete" || second.Updated != 1 {
		t.Fatalf("replacement scan = %+v", second)
	}
	status := waitVerificationRunState(t, service, second.ID, "complete")
	if status.Total != 1 || status.Verified != 1 || status.Failed != 0 || status.Unverified != 0 {
		t.Fatalf("replacement verification = %+v", status)
	}
	var runID, itemStatus string
	if err := service.db.QueryRowContext(t.Context(), `SELECT run_id,status FROM library_verification_items`).Scan(&runID, &itemStatus); err != nil {
		t.Fatal(err)
	}
	if runID != second.ID || itemStatus != "verified" || runID == first.ID {
		t.Fatalf("verification fence run=%q status=%q first=%q second=%q", runID, itemStatus, first.ID, second.ID)
	}
}

func TestConfiguredRootPathChangeInvalidatesPendingVerification(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writeTestWAV(t, filepath.Join(firstRoot, "song.wav"), 4_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: firstRoot}})
	t.Setenv(verificationHelperEnvironment, "hold")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}
	scan, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	waitScan(t, service, scan.ID)
	waitVerificationState(t, service, "running")
	if err := service.SetRoots([]Root{{ID: "music", Name: "Music", Path: secondRoot}}); err != nil {
		t.Fatal(err)
	}
	status, err := service.VerificationStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "idle" || status.Total != 0 || status.Pending != 0 {
		t.Fatalf("root-change verification = %+v", status)
	}
	var items int
	if err := service.db.QueryRowContext(t.Context(), `SELECT count(*) FROM library_verification_items`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 0 {
		t.Fatalf("verification items after root change = %d", items)
	}
}

func TestAddingConfiguredRootPreservesCompletedVerification(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writeTestWAV(t, filepath.Join(firstRoot, "retained.wav"), 4_000)
	writeTestWAV(t, filepath.Join(secondRoot, "new.wav"), 4_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "first", Name: "First", Path: firstRoot}})
	t.Setenv(verificationHelperEnvironment, "valid")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}
	first, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, first.ID); completed.Status != "complete" {
		t.Fatalf("initial scan = %+v", completed)
	}
	if status := waitVerificationRunState(t, service, first.ID, "complete"); status.Total != 1 || status.Verified != 1 {
		t.Fatalf("initial verification = %+v", status)
	}
	if err := service.SetRoots([]Root{
		{ID: "first", Name: "First renamed", Path: firstRoot},
		{ID: "second", Name: "Second", Path: secondRoot},
	}); err != nil {
		t.Fatal(err)
	}
	status, err := service.VerificationStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "complete" || status.Total != 1 || status.Verified != 1 {
		t.Fatalf("verification after adding root = %+v", status)
	}
	var rootName string
	if err := service.db.QueryRowContext(t.Context(), `SELECT root_name FROM library_verification_items`).Scan(&rootName); err != nil {
		t.Fatal(err)
	}
	if rootName != "First renamed" {
		t.Fatalf("retained verification root name = %q", rootName)
	}

	t.Setenv(verificationHelperEnvironment, "corrupt")
	second, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, second.ID); completed.Status != "complete" || completed.Added != 1 || completed.Updated != 0 {
		t.Fatalf("added-root scan = %+v", completed)
	}
	status = waitVerificationRunState(t, service, second.ID, "complete")
	if status.Total != 2 || status.Verified != 1 || status.Failed != 1 || status.Unverified != 0 {
		t.Fatalf("added-root verification = %+v", status)
	}
}

func TestVerificationProgressRecoversAfterInterruptedWorker(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "library.sqlite")
	cachePath := filepath.Join(directory, "artwork")
	rootConfig := []Root{{ID: "music", Name: "Music", Path: root}}

	firstContext, stopFirst := context.WithCancel(context.Background())
	firstDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	firstDB.SetMaxOpenConns(1)
	first, err := New(firstContext, firstDB, rootConfig, cachePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(verificationHelperEnvironment, "hold")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.StartVerification(VerificationOptions{FFmpegPath: executable}); err != nil {
		t.Fatal(err)
	}
	scan, err := first.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	waitScan(t, first, scan.ID)
	waitVerificationState(t, first, "running")
	stopFirst()
	waitContext, cancelWait := context.WithTimeout(t.Context(), 2*time.Second)
	if err := first.WaitVerification(waitContext); err != nil {
		t.Fatal(err)
	}
	cancelWait()
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	secondContext, stopSecond := context.WithCancel(context.Background())
	secondDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	secondDB.SetMaxOpenConns(1)
	second, err := New(secondContext, secondDB, rootConfig, cachePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopSecond()
		wait, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = second.WaitVerification(wait)
		cancel()
		_ = secondDB.Close()
	})
	t.Setenv(verificationHelperEnvironment, "valid")
	if err := second.StartVerification(VerificationOptions{FFmpegPath: executable}); err != nil {
		t.Fatal(err)
	}
	status := waitVerificationState(t, second, "complete")
	if status.Total != 1 || status.Verified != 1 || status.Pending != 0 {
		t.Fatalf("recovered verification = %+v", status)
	}
}

func TestVerificationWithoutConfiguredEngineIsUnavailableNotHealthy(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	service, _ := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	scan, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scan = waitScan(t, service, scan.ID)
	if scan.Status != "complete" {
		t.Fatalf("scan = %+v", scan)
	}
	if err := service.StartVerification(VerificationOptions{}); err != nil {
		t.Fatal(err)
	}
	status, err := service.VerificationStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "unavailable" || status.Total != 1 || status.Pending != 1 || status.Verified != 0 || status.Error == "" {
		t.Fatalf("unconfigured verification = %+v", status)
	}
}

func TestDecoderErrorOutputPrecludesVerifiedOnZeroExit(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	t.Setenv(verificationHelperEnvironment, "stderr-success")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}
	scan, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scan = waitScan(t, service, scan.ID)
	status := waitVerificationRunState(t, service, scan.ID, "complete")
	if status.Failed != 1 || status.Verified != 0 {
		t.Fatalf("zero-exit decoder error = %+v", status)
	}
}

func TestFLACStreamInfoAndSignedNonByteAlignedPCM(t *testing.T) {
	// FLAC hashes signed, little-endian samples rounded up to whole bytes.
	expectedDigest := md5.Sum([]byte{0x00, 0xf8, 0xff, 0xff, 0x00, 0x00, 0xff, 0x07})
	path := filepath.Join(t.TempDir(), "sample.flac")
	stream := makeFLACStreamInfo(48_000, 1, 12, 4, expectedDigest)
	if err := os.WriteFile(path, stream, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := readFLACStreamInfo(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	decoded := new(bytes.Buffer)
	for _, value := range []int32{-2048, -1, 0, 2047} {
		if err := binary.Write(decoded, binary.LittleEndian, value<<20); err != nil {
			t.Fatal(err)
		}
	}
	frames, digest, err := readDecodedPCM(decoded, &info)
	if err != nil {
		t.Fatal(err)
	}
	if frames != 4 || digest != expectedDigest {
		t.Fatalf("decoded frames=%d digest=%x expected=%x", frames, digest, expectedDigest)
	}
}

func TestFLACDeclaredSamplesAndChecksumDetermineIntegrity(t *testing.T) {
	validChecksum := md5.Sum([]byte{0, 0})
	for _, test := range []struct {
		name             string
		total            uint64
		checksum         [md5.Size]byte
		expectedCode     string
		expectedVerified int64
		expectedFailed   int64
	}{
		{name: "valid", total: 1, checksum: validChecksum, expectedVerified: 1},
		{name: "checksum unavailable", total: 1, expectedVerified: 1},
		{name: "sample count mismatch", total: 2, expectedCode: "FLAC_SAMPLE_COUNT_MISMATCH", expectedFailed: 1},
		{name: "checksum mismatch", total: 1, checksum: [md5.Size]byte{1}, expectedCode: "FLAC_CHECKSUM_MISMATCH", expectedFailed: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "sample.flac"), makeFLACStreamInfo(48_000, 1, 16, test.total, test.checksum), 0o600); err != nil {
				t.Fatal(err)
			}
			service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
			t.Setenv(verificationHelperEnvironment, "valid")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, IsPlaybackActive: playback.Load}); err != nil {
				t.Fatal(err)
			}
			scan, err := service.StartScan(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			scan = waitScan(t, service, scan.ID)
			if scan.Status != "complete" {
				t.Fatalf("scan = %+v", scan)
			}
			status := waitVerificationRunState(t, service, scan.ID, "complete")
			if status.Failed != test.expectedFailed || status.Verified != test.expectedVerified {
				t.Fatalf("verification = %+v", status)
			}
			result := service.decodeFile(t.Context(), mustOpenTrack(t, service), verificationItem{format: "flac"})
			if result.code != test.expectedCode {
				t.Fatalf("code = %q, want %q", result.code, test.expectedCode)
			}
		})
	}
}

func makeFLACStreamInfo(sampleRate uint64, channels, bits int, total uint64, checksum [md5.Size]byte) []byte {
	data := make([]byte, 42)
	copy(data[:4], "fLaC")
	data[4] = 0x80
	data[7] = 34
	binary.BigEndian.PutUint16(data[8:10], 4096)
	binary.BigEndian.PutUint16(data[10:12], 4096)
	packed := sampleRate<<44 | uint64(channels-1)<<41 | uint64(bits-1)<<36 | total
	binary.BigEndian.PutUint64(data[18:26], packed)
	copy(data[26:42], checksum[:])
	return data
}

func mustOpenTrack(t *testing.T, service *Service) *os.File {
	t.Helper()
	page, err := service.Browse(t.Context(), Query{Kind: "tracks", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	tracks := page.Items.([]Track)
	if len(tracks) != 1 {
		t.Fatalf("tracks = %#v", tracks)
	}
	file, _, err := service.Open(t.Context(), tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func newVerificationTestService(t *testing.T, roots []Root) (*Service, *atomic.Bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	service, err := New(ctx, db, roots, filepath.Join(t.TempDir(), "artwork"), nil)
	if err != nil {
		cancel()
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		wait, stop := context.WithTimeout(context.Background(), 2*time.Second)
		_ = service.WaitVerification(wait)
		stop()
		_ = db.Close()
	})
	return service, &atomic.Bool{}
}

func waitVerificationState(t *testing.T, service *Service, state string) VerificationStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := service.VerificationStatus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.State == state {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	status, err := service.VerificationStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("verification state = %+v, want %q", status, state)
	return VerificationStatus{}
}

func waitVerificationRunState(t *testing.T, service *Service, runID, state string) VerificationStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var currentRun, currentState string
		if err := service.db.QueryRowContext(t.Context(), `SELECT run_id,state FROM library_verification_state WHERE singleton=1`).Scan(&currentRun, &currentState); err != nil {
			t.Fatal(err)
		}
		if currentRun == runID && currentState == state {
			status, err := service.VerificationStatus(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	status, err := service.VerificationStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("verification run state = %+v, want run %q state %q", status, runID, state)
	return VerificationStatus{}
}

func waitIntegrityHistory(t *testing.T, history *errorhistory.Service, total int) errorhistory.ListResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		records, err := history.List(t.Context(), errorhistory.ListOptions{Kind: "integrity", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if records.Total == total {
			return records
		}
		time.Sleep(5 * time.Millisecond)
	}
	records, err := history.List(t.Context(), errorhistory.ListOptions{Kind: "integrity", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("integrity history total = %d, want %d", records.Total, total)
	return errorhistory.ListResult{}
}

func TestVerificationFailureAndHistoryCommitTogether(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	history, err := errorhistory.New(t.Context(), service.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`CREATE TRIGGER reject_integrity_history BEFORE INSERT ON error_history BEGIN SELECT RAISE(FAIL, 'injected history storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	t.Setenv(verificationHelperEnvironment, "corrupt")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, History: history, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}
	first, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if scan := waitScan(t, service, first.ID); scan.Status != "complete" {
		t.Fatalf("metadata scan = %+v", scan)
	}
	status := waitVerificationState(t, service, "unavailable")
	if status.Failed != 0 || status.Pending != 1 {
		t.Fatalf("file result committed without its history: %+v", status)
	}
	records, err := history.List(t.Context(), errorhistory.ListOptions{Limit: 10})
	if err != nil || records.Total != 0 {
		t.Fatalf("failed history transaction leaked a record: %+v, %v", records, err)
	}
	if _, err := service.db.Exec("DROP TRIGGER reject_integrity_history"); err != nil {
		t.Fatal(err)
	}
	second, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if scan := waitScan(t, service, second.ID); scan.Status != "complete" {
		t.Fatalf("retry metadata scan = %+v", scan)
	}
	status = waitVerificationRunState(t, service, second.ID, "complete")
	records, err = history.List(t.Context(), errorhistory.ListOptions{Limit: 10})
	if err != nil || status.Failed != 1 || status.Pending != 0 || records.Total != 1 || len(records.Items) != 1 {
		t.Fatalf("retry did not commit the failure and history together: %+v, %+v, %v", status, records, err)
	}
}

func TestInterruptedRescanDoesNotRetryTheSameFileForever(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	service, playback := newVerificationTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	history, err := errorhistory.New(t.Context(), service.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	playback.Store(true)
	t.Setenv(verificationHelperEnvironment, "valid")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartVerification(VerificationOptions{FFmpegPath: executable, History: history, IsPlaybackActive: playback.Load}); err != nil {
		t.Fatal(err)
	}
	scan, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, scan.ID); completed.Status != "complete" {
		t.Fatalf("metadata scan = %+v", completed)
	}
	waitVerificationState(t, service, "paused")
	// A cancelled rescan can leave this already-indexed row behind without
	// completing a new full-library verification schedule.
	if _, err := service.db.Exec("UPDATE library_tracks SET last_seen_scan='interrupted-rescan'"); err != nil {
		t.Fatal(err)
	}
	playback.Store(false)
	status := waitVerificationState(t, service, "complete")
	if status.Pending != 0 || status.Verified != 0 || status.Failed != 0 || status.Unverified != 1 {
		t.Fatalf("stale verification neither settled honestly nor stopped retrying: %+v", status)
	}
	records, err := history.List(t.Context(), errorhistory.ListOptions{Kind: "integrity", Limit: 10})
	if err != nil || len(records.Items) != 1 || records.Items[0].Code != "FILE_CHANGED" || records.Items[0].Outcome != "unknown" {
		t.Fatalf("changed-file result = %+v, %v", records, err)
	}
}

func TestVerificationDistinguishesInputAndEngineFailuresFromCorruption(t *testing.T) {
	for _, test := range []struct {
		stderr, status, code string
	}{
		{"Input/output error; Invalid data found when processing input", "unverified", "ENGINE_RESOURCE_ERROR"},
		{"Could not find codec parameters; no decoder found for this codec", "unverified", "FORMAT_UNSUPPORTED"},
		{"CRC mismatch in audio frame", "failed", "AUDIO_DECODE_CORRUPT"},
	} {
		result := classifyDecodeFailure(test.stderr, "flac")
		if result.status != test.status || result.code != test.code {
			t.Fatalf("%q classified as %+v", test.stderr, result)
		}
	}
	file, err := os.CreateTemp(t.TempDir(), "*.flac")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	result := (&Service{}).decodeFile(t.Context(), file, verificationItem{format: "flac"})
	if result.status != "unverified" || result.code != "FILE_UNAVAILABLE" {
		t.Fatalf("an unreadable descriptor was reported as corrupt content: %+v", result)
	}
}
