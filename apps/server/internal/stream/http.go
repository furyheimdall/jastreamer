package stream

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
)

type byteRange struct {
	start  int64
	length int64
}

func (service *Service) Handler() http.Handler {
	return http.HandlerFunc(service.serveHTTP)
}

func (service *Service) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	token, artwork, ok := mediaRequest(request.URL.Path)
	if !ok {
		writeStreamError(writer, http.StatusNotFound, "MEDIA_NOT_FOUND")
		return
	}
	service.mu.Lock()
	value := service.byToken[token]
	service.mu.Unlock()
	if value == nil {
		writeStreamError(writer, http.StatusNotFound, "MEDIA_NOT_FOUND")
		return
	}
	remoteIP, err := rendererIP(request.RemoteAddr)
	if err != nil || remoteIP != value.sourceIP {
		writeStreamError(writer, http.StatusForbidden, "MEDIA_FORBIDDEN")
		return
	}
	if artwork && value.artwork == nil {
		writeStreamError(writer, http.StatusNotFound, "MEDIA_NOT_FOUND")
		return
	}
	if value.cast {
		if !serveCastCORS(writer, request) {
			return
		}
	} else if !allowSameOriginMediaRequest(request) {
		writeStreamError(writer, http.StatusForbidden, "ORIGIN_NOT_ALLOWED")
		return
	}
	if request.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		allowed := "GET, HEAD"
		if value.cast {
			allowed += ", OPTIONS"
		}
		writer.Header().Set("Allow", allowed)
		writeStreamError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	stopBindingCancellation := context.AfterFunc(value.ctx, cancel)
	stopWriteCancellation := interruptWriteOnCancel(ctx, writer)
	defer func() {
		stopBindingCancellation()
		stopWriteCancellation()
		cancel()
	}()
	if artwork {
		service.serveArtwork(writer, request, ctx, value)
		return
	}
	file, track, err := service.openBound(ctx, value)
	if err != nil {
		writeStreamError(writer, streamErrorStatus(err), streamErrorCode(err))
		return
	}
	activeID, registered := service.register(value, file)
	if !registered {
		_ = file.Close()
		writeStreamError(writer, http.StatusNotFound, "MEDIA_NOT_FOUND")
		return
	}
	defer service.release(value, activeID, file)

	writer.Header().Set("Cache-Control", "private, no-store")
	if value.representation.transformed {
		service.serveTransformed(writer, request, ctx, file, value.representation)
		return
	}
	service.serveOriginal(writer, request, ctx, file, track.Size, value.representation.mime)
}

func serveCastCORS(writer http.ResponseWriter, request *http.Request) bool {
	origin, present, valid := corsOrigin(request)
	if !valid {
		writeStreamError(writer, http.StatusForbidden, "ORIGIN_NOT_ALLOWED")
		return false
	}
	if request.Method == http.MethodOptions {
		if !present || !validCastPreflight(request) {
			writeStreamError(writer, http.StatusForbidden, "ORIGIN_NOT_ALLOWED")
			return false
		}
		writer.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writer.Header().Set("Access-Control-Allow-Methods", "GET, HEAD")
		writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept-Encoding, Range")
		writer.Header().Set("Access-Control-Max-Age", "600")
	} else if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return true
	}
	if present {
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Add("Vary", "Origin")
		writer.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range, Content-Type")
	}
	return true
}

func validCastPreflight(request *http.Request) bool {
	methods := request.Header.Values("Access-Control-Request-Method")
	if len(methods) != 1 || (methods[0] != http.MethodGet && methods[0] != http.MethodHead) {
		return false
	}
	for _, line := range request.Header.Values("Access-Control-Request-Headers") {
		for value := range strings.SplitSeq(line, ",") {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "content-type", "accept-encoding", "range":
			default:
				return false
			}
		}
	}
	return true
}

func allowSameOriginMediaRequest(request *http.Request) bool {
	if request.Method == http.MethodOptions || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin, present, valid := corsOrigin(request)
	if !valid || !present {
		return valid
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	parsed, _ := url.Parse(origin)
	return parsed.Scheme == scheme && strings.EqualFold(parsed.Host, request.Host)
}

func corsOrigin(request *http.Request) (string, bool, bool) {
	values := request.Header.Values("Origin")
	if len(values) == 0 {
		return "", false, true
	}
	if len(values) != 1 {
		return "", true, false
	}
	origin := strings.TrimSpace(values[0])
	if origin == "" || strings.Contains(origin, ",") {
		return "", true, false
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", true, false
	}
	return origin, true, true
}

func interruptWriteOnCancel(ctx context.Context, writer http.ResponseWriter) func() {
	controller := http.NewResponseController(writer)
	done := make(chan struct{})
	deadlineApplied := false
	stop := context.AfterFunc(ctx, func() {
		deadlineApplied = controller.SetWriteDeadline(time.Now()) == nil
		close(done)
	})
	return func() {
		if stop() {
			return
		}
		<-done
		if deadlineApplied {
			_ = controller.SetWriteDeadline(time.Time{})
		}
	}
}

func mediaRequest(path string) (string, bool, bool) {
	if !strings.HasPrefix(path, "/media/") {
		return "", false, false
	}
	value := strings.TrimPrefix(path, "/media/")
	artwork := strings.HasSuffix(value, "/artwork")
	if artwork {
		value = strings.TrimSuffix(value, "/artwork")
	}
	if value == "" || strings.Contains(value, "/") {
		return "", false, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return value, artwork, err == nil && len(decoded) == tokenBytes
}

func (service *Service) serveArtwork(writer http.ResponseWriter, request *http.Request, ctx context.Context, value *binding) {
	file, mime, size, err := service.openBoundArtwork(ctx, value)
	if err != nil {
		writeStreamError(writer, http.StatusNotFound, "MEDIA_NOT_FOUND")
		return
	}
	activeID, registered := service.register(value, file)
	if !registered {
		_ = file.Close()
		writeStreamError(writer, http.StatusNotFound, "MEDIA_NOT_FOUND")
		return
	}
	defer service.release(value, activeID, file)
	writer.Header().Set("Cache-Control", "private, no-store")
	service.serveOriginal(writer, request, ctx, file, size, mime)
}

func (service *Service) openBoundArtwork(ctx context.Context, value *binding) (*os.File, string, int64, error) {
	if value.artwork == nil {
		return nil, "", 0, ErrTrackUnavailable
	}
	audio, track, err := service.openBound(ctx, value)
	if err != nil {
		return nil, "", 0, err
	}
	if closeErr := audio.Close(); closeErr != nil || track.ArtworkID != value.artwork.id {
		return nil, "", 0, ErrTrackUnavailable
	}
	file, mime, err := service.library.Artwork(ctx, value.artwork.id)
	if err != nil {
		return nil, "", 0, ErrTrackUnavailable
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 || mime != value.artwork.mime ||
		!os.SameFile(value.artwork.fileInfo, info) || value.artwork.fileInfo.Size() != info.Size() ||
		value.artwork.fileInfo.ModTime() != info.ModTime() {
		_ = file.Close()
		return nil, "", 0, ErrTrackUnavailable
	}
	return file, mime, info.Size(), nil
}

func (service *Service) openBound(ctx context.Context, value *binding) (*os.File, library.Track, error) {
	file, track, err := service.library.Open(ctx, value.trackID)
	if err != nil {
		return nil, library.Track{}, ErrTrackUnavailable
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, library.Track{}, ErrTrackUnavailable
	}
	if track.ID != value.trackID || !track.Available || track.Size != info.Size() {
		_ = file.Close()
		return nil, library.Track{}, ErrTrackUnavailable
	}
	if !os.SameFile(value.fileInfo, info) || value.fileInfo.Size() != info.Size() || value.fileInfo.ModTime() != info.ModTime() {
		_ = file.Close()
		return nil, library.Track{}, ErrStaleMedia
	}
	return file, track, nil
}

func (service *Service) serveOriginal(writer http.ResponseWriter, request *http.Request, ctx context.Context, file *os.File, size int64, mime string) {
	writer.Header().Set("Content-Type", mime)
	writer.Header().Set("Accept-Ranges", "bytes")
	selected := byteRange{start: 0, length: size}
	status := http.StatusOK
	if header := request.Header.Get("Range"); header != "" {
		var err error
		selected, err = parseRange(header, size)
		if err != nil {
			writer.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
			writeStreamError(writer, http.StatusRequestedRangeNotSatisfiable, "MEDIA_RANGE_UNSATISFIABLE")
			return
		}
		status = http.StatusPartialContent
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", selected.start, selected.start+selected.length-1, size))
	}
	if selected.start != 0 {
		if _, err := file.Seek(selected.start, io.SeekStart); err != nil {
			writeStreamError(writer, http.StatusInternalServerError, "MEDIA_UNAVAILABLE")
			return
		}
	}
	writer.Header().Set("Content-Length", strconv.FormatInt(selected.length, 10))
	writer.WriteHeader(status)
	if request.Method == http.MethodHead || selected.length == 0 {
		return
	}
	_ = copyFileRange(ctx, writer, file, selected.length)
}

func (service *Service) serveTransformed(writer http.ResponseWriter, request *http.Request, ctx context.Context, file *os.File, selected representation) {
	// Chromium opens even nonseekable media with a byte-zero range. A complete
	// 200 response is valid for that initial stream; nonzero seeks remain denied.
	initialCastRange := selected.transcode == transcodeWAV && request.Header.Get("Range") == "bytes=0-"
	if request.Header.Get("Range") != "" && !initialCastRange {
		writeStreamError(writer, http.StatusRequestedRangeNotSatisfiable, "MEDIA_RANGE_UNSUPPORTED")
		return
	}
	writer.Header().Set("Content-Type", selected.mime)
	if request.Method == http.MethodHead {
		writer.WriteHeader(http.StatusOK)
		return
	}
	stream, err := service.ffmpeg.open(ctx, file, selected.transcode)
	if err != nil {
		writeStreamError(writer, streamErrorStatus(err), streamErrorCode(err))
		return
	}
	defer stream.Close()
	writer.WriteHeader(http.StatusOK)
	_ = copyStream(ctx, writer, stream)
}

func parseRange(value string, size int64) (byteRange, error) {
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") || size <= 0 {
		return byteRange{}, ErrRangeNotSatisfiable
	}
	left, right, found := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !found || strings.Contains(right, "-") || left == "" && right == "" {
		return byteRange{}, ErrRangeNotSatisfiable
	}
	if left == "" {
		suffix, err := strconv.ParseInt(right, 10, 64)
		if err != nil || suffix <= 0 {
			return byteRange{}, ErrRangeNotSatisfiable
		}
		if suffix > size {
			suffix = size
		}
		return byteRange{start: size - suffix, length: suffix}, nil
	}
	start, err := strconv.ParseInt(left, 10, 64)
	if err != nil || start < 0 || start >= size {
		return byteRange{}, ErrRangeNotSatisfiable
	}
	end := size - 1
	if right != "" {
		end, err = strconv.ParseInt(right, 10, 64)
		if err != nil || end < start {
			return byteRange{}, ErrRangeNotSatisfiable
		}
		if end >= size {
			end = size - 1
		}
	}
	return byteRange{start: start, length: end - start + 1}, nil
}

func copyFileRange(ctx context.Context, destination io.Writer, source io.Reader, remaining int64) error {
	var buffer [64 * 1024]byte
	for remaining > 0 {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		chunk := int64(len(buffer))
		if remaining < chunk {
			chunk = remaining
		}
		count, readErr := source.Read(buffer[:chunk])
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			remaining -= int64(written)
			if writeErr != nil {
				return writeErr
			}
			if written != count {
				return io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && remaining == 0 {
				return nil
			}
			return readErr
		}
	}
	return nil
}

func copyStream(ctx context.Context, destination io.Writer, source io.Reader) error {
	var buffer [64 * 1024]byte
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		count, readErr := source.Read(buffer[:])
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			if writeErr != nil {
				return writeErr
			}
			if written != count {
				return io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func streamErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrTrackUnavailable):
		return http.StatusNotFound
	case errors.Is(err, ErrStaleMedia):
		return http.StatusConflict
	case errors.Is(err, ErrUnsupportedMedia):
		return http.StatusNotAcceptable
	case errors.Is(err, ErrRangeNotSatisfiable):
		return http.StatusRequestedRangeNotSatisfiable
	case errors.Is(err, ErrTranscodeBusy):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func streamErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTrackUnavailable):
		return "MEDIA_NOT_FOUND"
	case errors.Is(err, ErrStaleMedia):
		return "MEDIA_STALE"
	case errors.Is(err, ErrUnsupportedMedia):
		return "MEDIA_UNSUPPORTED"
	case errors.Is(err, ErrRangeNotSatisfiable):
		return "MEDIA_RANGE_UNSATISFIABLE"
	case errors.Is(err, ErrTranscodeBusy):
		return "MEDIA_TRANSCODER_BUSY"
	default:
		return "MEDIA_UNAVAILABLE"
	}
}

func writeStreamError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Del("Content-Length")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, code+"\n")
}
