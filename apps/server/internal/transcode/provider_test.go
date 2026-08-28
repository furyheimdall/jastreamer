//go:build !windows

package transcode

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func TestProvider_streams_five_codecs_as_fixed_L16_with_structured_arguments(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramStream)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: 10 * time.Second})
	formats := []catalog.Format{catalog.FormatFLAC, catalog.FormatMP3, catalog.FormatOggVorbis, catalog.FormatOpus, catalog.FormatPCMWAV}

	for _, format := range formats {
		t.Run(string(format), func(t *testing.T) {
			// When
			stream, err := provider.Open(t.Context(), bytes.NewReader([]byte("encoded")), format)
			if err != nil {
				t.Fatal(err)
			}
			output, readErr := io.ReadAll(stream)
			closeErr := stream.Close()

			// Then
			if err := errors.Join(readErr, closeErr); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(output, []byte{0x12, 0x34, 0xfe, 0xdc}) {
				t.Fatalf("output = %x", output)
			}
		})
	}
}

func TestProvider_returns_start_failure_when_executable_cannot_launch(t *testing.T) {
	// Given
	path := fakeExecutable(t, "#!/definitely/missing-interpreter\n")
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})

	// When
	_, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)

	// Then
	var processError *ProcessError
	if !errors.As(err, &processError) || processError.Operation != "start" {
		t.Fatalf("error = %#v", err)
	}
}

func TestProvider_bounds_stderr_and_returns_start_failure(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramStderrFailure)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})

	// When
	_, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatMP3)

	// Then
	var processError *ProcessError
	if !errors.As(err, &processError) || len(processError.Detail) > maxDiagnosticBytes || bytes.Contains([]byte(processError.Detail), []byte(path)) {
		t.Fatalf("error = %#v", err)
	}
}

func TestProvider_third_concurrent_transcode_fails_immediately_without_spawning_and_capacity_releases_once(t *testing.T) {
	// Given
	launches := filepath.Join(t.TempDir(), "launches")
	path := fakeExecutable(t, fakeProgramBlocking)
	provider := newTestProvider(t, Config{
		Approval: availableApproval(t, path), StartTimeout: time.Second,
		environment: append(os.Environ(), "LAUNCHES="+launches),
	})
	first, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatMP3)
	if err != nil {
		t.Fatal(err)
	}

	// When
	_, thirdErr := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatOpus)

	// Then
	var capacityErr *CapacityError
	if !errors.As(thirdErr, &capacityErr) || capacityErr.Limit != 2 {
		t.Fatalf("third open error = %#v", thirdErr)
	}
	launchBytes, err := os.ReadFile(launches)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(launchBytes), "launch\n") != 2 {
		t.Fatalf("launches = %q", launchBytes)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatOpus)
	if err != nil {
		t.Fatalf("capacity was not released exactly once: %v", err)
	}
	_, fourthErr := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatPCMWAV)
	if !errors.As(fourthErr, &capacityErr) {
		t.Fatalf("fourth open error = %#v", fourthErr)
	}
	_ = replacement.Close()
	_ = second.Close()
}

func TestProvider_places_validated_output_seek_after_input(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramSeek)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})

	// When
	stream, err := provider.OpenAt(t.Context(), Input{Source: bytes.NewReader([]byte("encoded")), Format: catalog.FormatMP3, Offset: 1250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	output, readErr := io.ReadAll(stream)
	closeErr := stream.Close()

	// Then
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if string(output) != "seeked" {
		t.Fatalf("output = %q", output)
	}
}

func TestProvider_rejects_seek_offset_outside_fixed_bounds_without_spawning(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramSeek)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})

	for _, offset := range []time.Duration{-time.Nanosecond, 24*time.Hour + time.Nanosecond} {
		// When
		_, openErr := provider.OpenAt(t.Context(), Input{Source: bytes.NewReader(nil), Format: catalog.FormatFLAC, Offset: offset})

		// Then
		var offsetErr *OffsetError
		if !errors.As(openErr, &offsetErr) {
			t.Fatalf("offset %s error = %#v", offset, openErr)
		}
	}
}

func TestProvider_returns_stream_when_process_exits_immediately_after_first_byte(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramFastSuccess)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})

	for range 32 {
		// When
		stream, openErr := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)
		if openErr != nil {
			t.Fatal(openErr)
		}
		output, readErr := io.ReadAll(stream)
		closeErr := stream.Close()

		// Then
		if err := errors.Join(readErr, closeErr); err != nil {
			t.Fatal(err)
		}
		if string(output) != "ok" {
			t.Fatalf("output = %q", output)
		}
	}
}

func TestProvider_launches_approved_executable_when_file_written_in_place_after_configuration(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramStream)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})
	if err := os.WriteFile(path, []byte(fakeProgramHung), 0o700); err != nil {
		t.Fatal(err)
	}

	// When
	stream, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)
	if err != nil {
		t.Fatal(err)
	}
	output, readErr := io.ReadAll(stream)
	closeErr := stream.Close()

	// Then
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, []byte{0x12, 0x34, 0xfe, 0xdc}) {
		t.Fatalf("output = %x", output)
	}
}

func TestProvider_start_timeout_terminates_silent_process(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramHung)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: 100 * time.Millisecond})

	// When
	_, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatPCMWAV)

	// Then
	if !errors.Is(err, ErrStartTimeout) {
		t.Fatalf("error = %v", err)
	}
}
