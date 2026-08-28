package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

type previousAdapter struct {
	uri       string
	playCalls int
	seek      []time.Duration
}

func (*previousAdapter) RendererID() playback.RendererID { return "previous-k17" }
func (*previousAdapter) ZoneID() playback.ZoneID         { return "previous-k17-zone" }
func (adapter *previousAdapter) SetAVTransportURI(_ context.Context, resource playback.MediaResource) error {
	adapter.uri = resource.URL
	return nil
}
func (adapter *previousAdapter) Play(context.Context) error { adapter.playCalls++; return nil }
func (*previousAdapter) Pause(context.Context) error        { return nil }
func (*previousAdapter) Stop(context.Context) error         { return nil }
func (adapter *previousAdapter) Seek(_ context.Context, position time.Duration) error {
	adapter.seek = append(adapter.seek, position)
	return nil
}

type previousUPnP struct{ adapter *previousAdapter }

func (*previousUPnP) Scan(context.Context) (upnp.ScanResult, error) { return upnp.ScanResult{}, nil }
func (*previousUPnP) LastScan() upnp.ScanResult                     { return upnp.ScanResult{} }
func (provider *previousUPnP) PlaybackAdapter(playback.RendererID, playback.ZoneID) (playback.K17PlaybackAdapter, error) {
	return provider.adapter, nil
}

type previousClock struct{ now time.Time }

func (clock previousClock) Now() time.Time { return clock.now }

type previousApplyFixture struct {
	service  *server
	store    *playback.Store
	adapter  *previousAdapter
	revision playback.Revision
}

func newPreviousApplyFixture(t *testing.T) previousApplyFixture {
	t.Helper()
	ctx := context.Background()
	directory := t.TempDir()
	store, err := playback.Open(ctx, playback.Config{
		Path: filepath.Join(directory, "playback.sqlite"), MigrationPath: "../../migrations/002_playback.sql",
		ExpansionPath: "../../migrations/003_todo12.sql", BackupDirectory: filepath.Join(directory, "backups"),
		SupportedSchema: playback.CurrentSchemaVersion, JournalMode: playback.JournalRollback,
	})
	if err != nil {
		t.Fatalf("open playback: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const zoneID playback.ZoneID = "previous-k17-zone"
	if _, err := store.CreateZone(ctx, playback.ZoneDefinition{ID: zoneID, DisplayName: "Previous K17"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	if _, err := store.UpsertK17Renderer(ctx, playback.K17Renderer{
		ID: "previous-k17", DisplayName: "K17", State: playback.RendererAvailable, Model: "K17",
		ProtocolInfo: "http-get:*:audio/mpeg:*", LastSeenAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create K17: %v", err)
	}
	if _, err := store.AssignRenderer(ctx, playback.AssignmentRequest{ZoneID: zoneID, RendererID: "previous-k17"}); err != nil {
		t.Fatalf("assign K17: %v", err)
	}
	enqueued, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: zoneID, IdempotencyKey: "seed", Tracks: []playback.QueueTrack{{ID: "first", Available: true}, {ID: "second", Available: true}},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	first, err := store.MutateTransport(ctx, playback.TransportMutationRequest{
		ZoneID: zoneID, IdempotencyKey: "start", ExpectedRevision: enqueued.Revision, Command: playback.TransportStart,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := store.ConfirmStart(ctx, zoneID, first.PlayID); err != nil {
		t.Fatalf("confirm first: %v", err)
	}
	second, err := store.CommitNext(ctx, playback.NextRequest{
		ZoneID: zoneID, Boundary: playback.Boundary{ID: "ended-first", PreviousPlayID: first.PlayID},
	})
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	snapshot, err := store.ConfirmStart(ctx, zoneID, second.PlayID)
	if err != nil {
		t.Fatalf("confirm second: %v", err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	signer, err := media.NewSigner(media.SignerConfig{
		KeyID: "previous", Key: []byte(strings.Repeat("k", 32)), Clock: previousClock{now: now}, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	catalogSnapshot := catalog.EmptySnapshot()
	catalogSnapshot.Tracks["first"] = catalog.Track{
		TrackID: "first", Available: true, Format: catalog.FormatMP3,
		FileVersion: catalog.FileVersion{Size: 1, Modified: now},
	}
	mediaService, err := media.NewService(media.ServiceConfig{
		Signer: signer, Authorizer: store,
		Snapshot: func(context.Context) catalog.Snapshot { return catalogSnapshot },
		Roots:    func(context.Context) map[catalog.RootID]string { return map[catalog.RootID]string{} },
	})
	if err != nil {
		t.Fatalf("create media service: %v", err)
	}
	adapter := &previousAdapter{}
	return previousApplyFixture{
		service: &server{config: Config{Queue: store, Media: mediaService, UPnP: &previousUPnP{adapter: adapter}, ServerHTTPSOrigin: ServerHTTPSOrigin{value: "https://127.0.0.1:8443"}}},
		store:   store, adapter: adapter, revision: snapshot.Revision,
	}
}

func Test_K17_previous_history_dispatches_media_play_instead_of_seek(t *testing.T) {
	// Given
	fixture := newPreviousApplyFixture(t)
	result, err := fixture.store.MutateTransport(context.Background(), playback.TransportMutationRequest{
		ZoneID: "previous-k17-zone", IdempotencyKey: "previous-history", ExpectedRevision: fixture.revision,
		Command: playback.TransportPrevious, PositionMS: 5_000,
	})
	if err != nil {
		t.Fatalf("previous mutation: %v", err)
	}
	request := httptest.NewRequest("POST", "https://server.test/api/v1/zones/previous-k17-zone/transport", nil)
	request.SetPathValue("zoneID", "previous-k17-zone")

	// When
	err = fixture.service.dispatchTransport(context.Background(), transportDispatch{
		request: request, command: playback.TransportPrevious, positionMS: 5_000, result: result,
	})

	// Then
	if err != nil || fixture.adapter.playCalls != 1 || fixture.adapter.uri == "" || len(fixture.adapter.seek) != 0 {
		t.Fatalf("K17 previous dispatch = play %d uri %q seek %+v (%v)", fixture.adapter.playCalls, fixture.adapter.uri, fixture.adapter.seek, err)
	}
}

func Test_K17_previous_above_boundary_dispatches_zero_seek(t *testing.T) {
	// Given
	fixture := newPreviousApplyFixture(t)
	result, err := fixture.store.MutateTransport(context.Background(), playback.TransportMutationRequest{
		ZoneID: "previous-k17-zone", IdempotencyKey: "previous-seek", ExpectedRevision: fixture.revision,
		Command: playback.TransportPrevious, PositionMS: 5_001,
	})
	if err != nil {
		t.Fatalf("previous mutation: %v", err)
	}
	request := httptest.NewRequest("POST", "https://server.test/api/v1/zones/previous-k17-zone/transport", nil)
	request.SetPathValue("zoneID", "previous-k17-zone")

	// When
	err = fixture.service.dispatchTransport(context.Background(), transportDispatch{
		request: request, command: playback.TransportPrevious, positionMS: 5_001, result: result,
	})

	// Then
	if err != nil || fixture.adapter.playCalls != 0 || fixture.adapter.uri != "" || len(fixture.adapter.seek) != 1 || fixture.adapter.seek[0] != 0 {
		t.Fatalf("K17 previous dispatch = play %d uri %q seek %+v (%v)", fixture.adapter.playCalls, fixture.adapter.uri, fixture.adapter.seek, err)
	}
}
