package media

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type byteRange struct {
	start  int64
	length int64
}

func (service *Service) Handler(expectedAudience Audience, expectedRenderer playback.RendererID) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		token := request.PathValue("token")
		if token == "" {
			token = strings.TrimPrefix(request.URL.Path, "/media/v1/")
		}
		claims, track, path, err := service.resolve(request.Context(), token, expectedAudience, expectedRenderer)
		if err != nil {
			writeMediaError(writer, err)
			return
		}
		if claims.Representation == L16 && request.Header.Get("Range") != "" {
			writeMediaError(writer, ErrRangeUnsupported)
			return
		}
		if claims.Representation == L16 && service.transformer == nil {
			writeMediaError(writer, ErrNoRepresentation)
			return
		}
		file, err := openValidated(path, claims)
		if err != nil {
			writeMediaError(writer, err)
			return
		}
		defer file.Close()
		untrack := service.track(claims, file)
		defer untrack()
		if claims.Representation == L16 {
			service.serveTransformed(writer, request, transformedResource{format: track.Format, source: file})
			return
		}
		service.serveOriginal(writer, request, originalResource{
			fingerprint: track.Fingerprint, size: claims.FileSize, format: track.Format, file: file,
		})
	})
}

func (service *Service) K17Handler() http.Handler {
	return service.Handler(AudienceK17Capability, "")
}

func MediaOnlyHandler(mediaHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /media/v1/{token}", mediaHandler)
	return mux
}

type originalResource struct {
	fingerprint string
	size        int64
	format      catalog.Format
	file        io.ReadSeeker
}

func (service *Service) serveOriginal(writer http.ResponseWriter, request *http.Request, resource originalResource) {
	mime, ok := mimeFor(resource.format)
	if !ok {
		writeMediaError(writer, ErrNoRepresentation)
		return
	}
	writer.Header().Set("Content-Type", mime)
	writer.Header().Set("Content-Length", strconv.FormatInt(resource.size, 10))
	writer.Header().Set("Accept-Ranges", "bytes")
	writer.Header().Set("ETag", `"`+resource.fingerprint+`"`)
	writer.Header().Set("Cache-Control", "private, max-age=600, immutable")
	rangeHeader := request.Header.Get("Range")
	if rangeHeader == "" {
		writer.WriteHeader(http.StatusOK)
		if request.Method == http.MethodGet {
			_, _ = io.CopyN(writer, resource.file, resource.size)
		}
		return
	}
	selected, err := parseRange(rangeHeader, resource.size)
	if err != nil {
		writer.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(resource.size, 10))
		writer.Header().Del("Content-Length")
		writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", selected.start, selected.start+selected.length-1, resource.size))
	writer.Header().Set("Content-Length", strconv.FormatInt(selected.length, 10))
	writer.WriteHeader(http.StatusPartialContent)
	if request.Method == http.MethodHead {
		return
	}
	if _, err := resource.file.Seek(selected.start, io.SeekStart); err != nil {
		return
	}
	_, _ = io.CopyN(writer, resource.file, selected.length)
}

type transformedResource struct {
	format catalog.Format
	source io.Reader
}

func (service *Service) serveTransformed(writer http.ResponseWriter, request *http.Request, resource transformedResource) {
	writer.Header().Set("Content-Type", "audio/L16;rate=44100;channels=2")
	writer.Header().Set("Cache-Control", "private, no-store")
	if request.Method == http.MethodHead {
		writer.WriteHeader(http.StatusOK)
		return
	}
	stream, err := service.transformer.Open(request.Context(), resource.source, resource.format)
	if err != nil {
		writeMediaError(writer, err)
		return
	}
	defer stream.Close()
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, stream)
}

func parseRange(value string, size int64) (byteRange, error) {
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") || size <= 0 {
		return byteRange{}, ErrRangeUnsupported
	}
	left, right, found := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !found || strings.Contains(right, "-") || (left == "" && right == "") {
		return byteRange{}, ErrRangeUnsupported
	}
	if left == "" {
		suffix, err := strconv.ParseInt(right, 10, 64)
		if err != nil || suffix <= 0 {
			return byteRange{}, ErrRangeUnsupported
		}
		if suffix > size {
			suffix = size
		}
		return byteRange{start: size - suffix, length: suffix}, nil
	}
	start, err := strconv.ParseInt(left, 10, 64)
	if err != nil || start < 0 || start >= size {
		return byteRange{}, ErrRangeUnsupported
	}
	end := size - 1
	if right != "" {
		end, err = strconv.ParseInt(right, 10, 64)
		if err != nil || end < start {
			return byteRange{}, ErrRangeUnsupported
		}
		if end >= size {
			end = size - 1
		}
	}
	return byteRange{start: start, length: end - start + 1}, nil
}

func writeMediaError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "MEDIA_UNAVAILABLE"
	switch {
	case errors.Is(err, ErrInvalidCapability), errors.Is(err, ErrExpiredCapability), errors.Is(err, ErrWrongAudience):
		status, code = http.StatusUnauthorized, "MEDIA_CAPABILITY_INVALID"
	case errors.Is(err, ErrWrongRenderer), errors.Is(err, ErrUnauthorizedPlay), errors.Is(err, ErrUnsafePath):
		status, code = http.StatusForbidden, "MEDIA_FORBIDDEN"
	case errors.Is(err, ErrTrackUnavailable):
		status, code = http.StatusNotFound, "MEDIA_NOT_FOUND"
	case errors.Is(err, ErrStaleFile):
		status, code = http.StatusConflict, "MEDIA_STALE"
	case errors.Is(err, ErrRangeUnsupported):
		status, code = http.StatusRequestedRangeNotSatisfiable, "MEDIA_RANGE_UNSUPPORTED"
	case errors.Is(err, ErrNoRepresentation):
		status, code = http.StatusNotAcceptable, "MEDIA_REPRESENTATION_UNAVAILABLE"
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(code + "\n"))
}
