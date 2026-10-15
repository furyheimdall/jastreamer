package raat

import (
	"encoding/binary"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func encodeTestSnapshot(s receiverSnapshot) []byte {
	data := []byte{s.state}
	for _, value := range []int64{s.position, s.written, s.samplesOut, s.fill, s.capacity, s.dropped, s.overrun, s.delay, s.deviceTime, s.startTime, s.epoch} {
		data = binary.BigEndian.AppendUint64(data, uint64(value))
	}
	return data
}

func TestReceiverSnapshotCountersAndEpoch(t *testing.T) {
	want := receiverSnapshot{state: 2, position: 24000, written: 48000, samplesOut: 24000, fill: 30000, capacity: 96000, delay: 10000000, deviceTime: 4000000000, startTime: 3500000000, epoch: 7}
	data := encodeTestSnapshot(want)
	got, err := parseSnapshot(data)
	if err != nil || got != want {
		t.Fatalf("snapshot = %+v, %v", got, err)
	}
	for _, bad := range [][]byte{data[:len(data)-1], append(append([]byte(nil), data...), 0), encodeTestSnapshot(receiverSnapshot{state: 2, fill: 4, capacity: 3}), encodeTestSnapshot(receiverSnapshot{state: 2, position: -1})} {
		if _, err := parseSnapshot(bad); err == nil {
			t.Fatal("accepted malformed snapshot")
		}
	}
}

func TestSigningPolicyHonorsActualAdvertisedKeys(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", " [ ] "} {
		if signingRequired(json.RawMessage(raw)) {
			t.Errorf("unsigned policy rejected %q", raw)
		}
	}
	for _, raw := range []string{`["vendor"]`, `[null]`, `{}`, `"key"`, `false`, `broken`} {
		if !signingRequired(json.RawMessage(raw)) {
			t.Errorf("accepted signing requirement %q", raw)
		}
	}
}

func TestNativeControlParsingUsesProvenSchemaAndOrder(t *testing.T) {
	controls, err := parseControls([]byte(`{"button":"next","seek":{"mode":"relative","seconds":-10}}`))
	want := []output.RemoteControl{{Action: "next"}, {Action: "seek", PositionMS: -10000, Relative: true}}
	if err != nil || !reflect.DeepEqual(controls, want) {
		t.Fatalf("controls = %+v, %v", controls, err)
	}
	for _, input := range []string{`{"state":"playing","seek":42}`, `{"seek":{"mode":"absolute","seconds":1.2}}`, `{"seek":{"mode":"absolute","seconds":-1}}`, `{"seek":{"mode":"relative","seconds":9223372036854775807}}`} {
		if _, err := parseControls([]byte(input)); err == nil {
			t.Errorf("accepted malformed control %s", input)
		}
	}
	for _, input := range []string{`{"action":"play"}`, `{"command":"pause"}`, `{"state":"playing"}`, `{"button":"unknown"}`, `{"seek":{"mode":"percent","seconds":10}}`} {
		got, err := parseControls([]byte(input))
		if err != nil || len(got) != 0 {
			t.Errorf("inferred command for %s: %+v, %v", input, got, err)
		}
	}
}

func TestNativeMetadataUsesSecondsAndNestedPrivateArtwork(t *testing.T) {
	resource := output.Resource{TrackID: "private-track", Title: "Title", Artist: "Artist", Album: "Album", DurationMS: 125999, ArtworkURL: "http://private.invalid/art"}
	data, err := nativeStatus(resource, audioFormat{"pcm", 48000, 24, 2}, TransportState{State: "PAUSED_PLAYBACK", RepeatMode: "one", Shuffle: true, PositionMS: 42099, CanPlay: true, CanSeek: true})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err = json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value["loop"] != "loopone" || value["state"] != "paused" || value["seek"] != float64(42) || value["shuffle"] != true || value["is_pause_allowed"] != false || value["is_seek_allowed"] != true {
		t.Fatalf("incorrect native status: %s", data)
	}
	now := value["now_playing"].(map[string]any)
	if now["length"] != float64(125) || now["artwork_url"] != nil || now["title"] != "Title" || now["three_line_subsubtitle"] != "Album" {
		t.Fatalf("incorrect metadata: %s", data)
	}
	if _, ok := value["position_ms"]; ok {
		t.Fatal("invented millisecond field")
	}
}

func TestReceiverFormatsPreserveNativeTypes(t *testing.T) {
	data := binary.BigEndian.AppendUint32(nil, featureOutput|featureTransport)
	data = binary.BigEndian.AppendUint32(data, 2)
	for _, format := range []audioFormat{{"pcm", 48000, 24, 2}, {"pcm_float64", 96000, 64, 2}} {
		setup := setupPayload(format, 100, 50, nil)
		data = append(data, setup[:9]...)
	}
	features, formats, err := receiverFormats(data)
	if err != nil || features != 3 || len(formats) != 2 || formats[1].sampleType != "pcm_float64" {
		t.Fatalf("formats=%+v features=%d err=%v", formats, features, err)
	}
	data[8] = 9
	if _, _, err := receiverFormats(data); err == nil {
		t.Fatal("unknown native type accepted")
	}
}
