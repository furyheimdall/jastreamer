package media

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type (
	SnapshotFunc func(context.Context) catalog.Snapshot
	RootsFunc    func(context.Context) map[catalog.RootID]string
)

type ServiceConfig struct {
	Signer      *Signer
	Authorizer  Authorizer
	Snapshot    SnapshotFunc
	Roots       RootsFunc
	Transformer TransformProvider
}

type Service struct {
	mu          sync.Mutex
	signer      *Signer
	authorizer  Authorizer
	snapshot    SnapshotFunc
	roots       RootsFunc
	transformer TransformProvider
	active      map[uint64]activeStream
	nextStream  uint64
}

type IssueRequest struct {
	BaseURL      string
	Audience     Audience
	RendererID   playback.RendererID
	ZoneID       playback.ZoneID
	PlayID       playback.PlayID
	TrackID      catalog.TrackID
	Capabilities []string
}

type IssuedMedia struct {
	URL            string
	MimeType       string
	TrackID        catalog.TrackID
	Title          string
	Representation Representation
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Signer == nil || config.Authorizer == nil || config.Snapshot == nil || config.Roots == nil {
		return nil, ErrInvalidConfig
	}
	return &Service{
		signer: config.Signer, authorizer: config.Authorizer, snapshot: config.Snapshot,
		roots: config.Roots, transformer: config.Transformer, active: map[uint64]activeStream{},
	}, nil
}

func (service *Service) Issue(ctx context.Context, request IssueRequest) (string, error) {
	issued, err := service.IssueMedia(ctx, request)
	return issued.URL, err
}

func (service *Service) IssueMedia(ctx context.Context, request IssueRequest) (IssuedMedia, error) {
	if err := service.authorizer.AuthorizeMedia(ctx, playback.MediaAuthorization{
		RendererID: request.RendererID, ZoneID: request.ZoneID, PlayID: request.PlayID, TrackID: playback.TrackID(request.TrackID),
	}); err != nil {
		return IssuedMedia{}, fmt.Errorf("authorize media issue: %w", ErrUnauthorizedPlay)
	}
	track, ok := service.snapshot(ctx).Tracks[request.TrackID]
	if !ok || !track.Available {
		return IssuedMedia{}, ErrTrackUnavailable
	}
	representation, err := Select(track.Format, request.Capabilities, service.transformer != nil)
	if err != nil {
		return IssuedMedia{}, err
	}
	mimeType, ok := MimeType(track.Format, representation)
	if !ok {
		return IssuedMedia{}, ErrNoRepresentation
	}
	base, err := url.Parse(request.BaseURL)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.RawQuery != "" || base.Fragment != "" {
		return IssuedMedia{}, ErrInvalidConfig
	}
	token, err := service.signer.Sign(Grant{
		Audience: request.Audience, RendererID: request.RendererID, ZoneID: request.ZoneID, PlayID: request.PlayID, TrackID: request.TrackID,
		Representation: representation, FileSize: track.FileVersion.Size, ModifiedNS: track.FileVersion.Modified.UnixNano(),
	})
	if err != nil {
		return IssuedMedia{}, err
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	switch request.Audience {
	case AudienceCustomRenderer:
		base.Path += "/api/v1/renderers/" + url.PathEscape(string(request.RendererID)) + "/media/" + token
	case AudienceK17Capability:
		base.Path += "/media/v1/" + token
	default:
		return IssuedMedia{}, ErrInvalidCapability
	}
	return IssuedMedia{URL: base.String(), MimeType: mimeType, TrackID: request.TrackID, Title: track.Metadata.Title, Representation: representation}, nil
}

func (service *Service) resolve(ctx context.Context, token string, expectedAudience Audience, expectedRenderer playback.RendererID) (Claims, catalog.Track, validatedFile, error) {
	claims, err := service.signer.Verify(token, expectedAudience, expectedRenderer)
	if err != nil {
		return Claims{}, catalog.Track{}, validatedFile{}, err
	}
	if err := service.authorizer.AuthorizeMedia(ctx, playback.MediaAuthorization{
		RendererID: claims.RendererID, ZoneID: claims.ZoneID, PlayID: claims.PlayID, TrackID: playback.TrackID(claims.TrackID),
	}); err != nil {
		return Claims{}, catalog.Track{}, validatedFile{}, ErrUnauthorizedPlay
	}
	track, ok := service.snapshot(ctx).Tracks[claims.TrackID]
	if !ok || !track.Available {
		return Claims{}, catalog.Track{}, validatedFile{}, ErrTrackUnavailable
	}
	if track.FileVersion.Size != claims.FileSize || track.FileVersion.Modified.UnixNano() != claims.ModifiedNS {
		return Claims{}, catalog.Track{}, validatedFile{}, ErrStaleFile
	}
	root, ok := service.roots(ctx)[track.RootID]
	if !ok {
		return Claims{}, catalog.Track{}, validatedFile{}, ErrUnsafePath
	}
	path, err := safeRegularPath(root, track.RelativePath, fileIdentity{size: claims.FileSize, modifiedNS: claims.ModifiedNS})
	if err != nil {
		return Claims{}, catalog.Track{}, validatedFile{}, err
	}
	return claims, track, path, nil
}
