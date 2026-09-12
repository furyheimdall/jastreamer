package stream

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

var (
	ErrInvalidConfig       = errors.New("stream: invalid configuration")
	ErrTrackUnavailable    = errors.New("stream: track is unavailable")
	ErrUnsupportedMedia    = errors.New("stream: renderer does not support this media")
	ErrStaleMedia          = errors.New("stream: media file changed")
	ErrRangeNotSatisfiable = errors.New("stream: byte range is not satisfiable")
	ErrTranscodeBusy       = errors.New("stream: transcoder capacity is exhausted")
	ErrTranscodeFailed     = errors.New("stream: transcoder failed")
)

const tokenBytes = 32

type Config struct {
	BaseURL    func(output.Device) (string, error)
	FFmpegPath string
	Transcode  bool
}

type libraryOpener interface {
	Open(context.Context, string) (*os.File, library.Track, error)
	Artwork(context.Context, string) (*os.File, string, error)
}

type Service struct {
	library libraryOpener
	baseURL func(output.Device) (string, error)
	ffmpeg  *transcoder

	mu         sync.Mutex
	byToken    map[string]*binding
	byPlay     map[string]*binding
	nextActive uint64
}

type boundArtwork struct {
	id       string
	mime     string
	fileInfo os.FileInfo
}

type binding struct {
	token          string
	playID         string
	trackID        string
	sourceIP       netip.Addr
	representation representation
	artwork        *boundArtwork
	fileInfo       os.FileInfo
	ctx            context.Context
	cancel         context.CancelFunc
	active         map[uint64]io.Closer
}

func New(lib *library.Service, config Config) (*Service, error) {
	if lib == nil {
		return nil, fmt.Errorf("%w: library service is required", ErrInvalidConfig)
	}
	return newService(lib, config)
}

func newService(lib libraryOpener, config Config) (*Service, error) {
	if lib == nil || config.BaseURL == nil {
		return nil, fmt.Errorf("%w: library service and base URL resolver are required", ErrInvalidConfig)
	}
	var ffmpeg *transcoder
	if config.Transcode {
		path, err := validateFFmpegPath(config.FFmpegPath)
		if err != nil {
			return nil, err
		}
		ffmpeg = newTranscoder(path)
	}
	return &Service{
		library: lib,
		baseURL: config.BaseURL,
		ffmpeg:  ffmpeg,
		byToken: make(map[string]*binding),
		byPlay:  make(map[string]*binding),
	}, nil
}

func validateFFmpegPath(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: FFmpeg path must be an absolute executable path", ErrInvalidConfig)
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%w: FFmpeg path is not an executable regular file", ErrInvalidConfig)
	}
	return clean, nil
}

func (service *Service) Prepare(ctx context.Context, device output.Device, track library.Track, playID string) (output.Resource, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := context.Cause(ctx); err != nil {
		return output.Resource{}, err
	}
	playID = strings.TrimSpace(playID)
	if playID == "" || track.ID == "" {
		return output.Resource{}, fmt.Errorf("%w: play and track identities are required", ErrTrackUnavailable)
	}
	sourceIP, err := rendererIP(device.Address)
	if err != nil {
		return output.Resource{}, fmt.Errorf("%w: renderer address is not a literal IP address", ErrInvalidConfig)
	}
	file, openedTrack, err := service.library.Open(ctx, track.ID)
	if err != nil {
		return output.Resource{}, fmt.Errorf("%w: open selected track", ErrTrackUnavailable)
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !info.Mode().IsRegular() || openedTrack.ID != track.ID || !openedTrack.Available || openedTrack.Size != info.Size() {
		return output.Resource{}, ErrTrackUnavailable
	}
	representation, err := selectRepresentation(openedTrack, device.ProtocolInfo, service.ffmpeg != nil)
	if err != nil {
		return output.Resource{}, err
	}
	base, err := service.resourceBaseURL(device)
	if err != nil {
		return output.Resource{}, err
	}
	token, err := randomToken()
	if err != nil {
		return output.Resource{}, fmt.Errorf("create media grant: %w", err)
	}
	artwork := service.inspectArtwork(ctx, openedTrack.ArtworkID)
	if err := context.Cause(ctx); err != nil {
		return output.Resource{}, err
	}
	bindingContext, cancel := context.WithCancel(context.Background())
	value := &binding{
		token:          token,
		playID:         playID,
		trackID:        openedTrack.ID,
		sourceIP:       sourceIP,
		representation: representation,
		artwork:        artwork,
		fileInfo:       info,
		ctx:            bindingContext,
		cancel:         cancel,
		active:         make(map[uint64]io.Closer),
	}

	service.mu.Lock()
	obsolete := service.byPlay[playID]
	if obsolete != nil {
		delete(service.byToken, obsolete.token)
	}
	service.byPlay[playID] = value
	service.byToken[token] = value
	service.mu.Unlock()
	service.cancelBinding(obsolete)

	mediaPath := strings.TrimRight(base.Path, "/") + "/media/" + token
	base.Path = mediaPath
	resourceSize := openedTrack.Size
	if representation.transformed {
		resourceSize = 0
	}
	resource := output.Resource{
		URL:        base.String(),
		Mime:       representation.mime,
		Title:      openedTrack.Title,
		Artist:     openedTrack.Artist,
		Album:      openedTrack.Album,
		DurationMS: openedTrack.DurationMS,
		Size:       resourceSize,
		Seekable:   !representation.transformed,
		TrackID:    openedTrack.ID,
		PlayID:     playID,
	}
	if artwork != nil {
		base.Path = mediaPath + "/artwork"
		resource.ArtworkURL = base.String()
	}
	return resource, nil
}

func (service *Service) Revoke(playID string) {
	service.mu.Lock()
	value := service.byPlay[playID]
	if value != nil {
		delete(service.byPlay, playID)
		delete(service.byToken, value.token)
	}
	service.mu.Unlock()
	service.cancelBinding(value)
}

func (service *Service) resourceBaseURL(device output.Device) (*url.URL, error) {
	value, err := service.baseURL(device)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve renderer media origin", ErrInvalidConfig)
	}
	base, err := url.Parse(value)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil ||
		(base.Path != "" && base.Path != "/") || base.RawPath != "" || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" {
		return nil, fmt.Errorf("%w: base URL must be an HTTP origin without credentials, path, query, or fragment", ErrInvalidConfig)
	}
	base.Path = ""
	return base, nil
}

func (service *Service) inspectArtwork(ctx context.Context, id string) *boundArtwork {
	if id == "" {
		return nil
	}
	file, mime, err := service.library.Artwork(ctx, id)
	if err != nil {
		return nil
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 || mime != "image/jpeg" {
		return nil
	}
	return &boundArtwork{id: id, mime: mime, fileInfo: info}
}

func randomToken() (string, error) {
	var value [tokenBytes]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func rendererIP(value string) (netip.Addr, error) {
	value = strings.TrimSpace(value)
	if address, ok := parseLiteralIP(value); ok {
		return address, nil
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		if address, ok := parseLiteralIP(host); ok {
			return address, nil
		}
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			if address, ok := parseLiteralIP(parsed.Hostname()); ok {
				return address, nil
			}
		}
	}
	return netip.Addr{}, errors.New("not a literal IP address")
}

func parseLiteralIP(value string) (netip.Addr, bool) {
	value = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(value), "]"), "[")
	if index := strings.LastIndexByte(value, '%'); index >= 0 {
		value = value[:index]
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func (service *Service) cancelBinding(value *binding) {
	if value == nil {
		return
	}
	value.cancel()
	service.mu.Lock()
	resources := make([]io.Closer, 0, len(value.active))
	for id, resource := range value.active {
		resources = append(resources, resource)
		delete(value.active, id)
	}
	service.mu.Unlock()
	for _, resource := range resources {
		_ = resource.Close()
	}
}

func (service *Service) register(value *binding, resource io.Closer) (uint64, bool) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.byToken[value.token] != value || service.byPlay[value.playID] != value {
		return 0, false
	}
	service.nextActive++
	id := service.nextActive
	value.active[id] = resource
	return id, true
}

func (service *Service) release(value *binding, id uint64, resource io.Closer) {
	service.mu.Lock()
	delete(value.active, id)
	service.mu.Unlock()
	_ = resource.Close()
}
