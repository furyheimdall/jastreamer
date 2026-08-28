package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type mediaRuntimeConfig struct {
	certificate tls.Certificate
	fingerprint string
	queue       *playback.Store
	catalog     *catalog.Coordinator
	transformer media.TransformProvider
}

func configureMediaService(ctx context.Context, config mediaRuntimeConfig) (*media.Service, error) {
	privateKey, err := x509.MarshalPKCS8PrivateKey(config.certificate.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("encode media wrapping identity: %w", err)
	}
	wrappingKey := sha256.Sum256(privateKey)
	signer, err := media.LoadOrCreateSigner(ctx, media.PersistentSignerConfig{
		Store: config.queue, WrappingKey: wrappingKey[:], WrappingKeyID: config.fingerprint,
		Clock: media.SystemClock{}, TTL: 10 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("load media signing key: %w", err)
	}
	service, err := media.NewService(media.ServiceConfig{
		Signer: signer, Authorizer: config.queue, Transformer: config.transformer,
		Snapshot: func(context.Context) catalog.Snapshot { return config.catalog.Snapshot() },
		Roots: func(context.Context) map[catalog.RootID]string {
			roots := config.catalog.Roots()
			paths := make(map[catalog.RootID]string, len(roots))
			for _, root := range roots {
				paths[root.ID] = root.CanonicalPath
			}
			return paths
		},
	})
	if err != nil {
		return nil, fmt.Errorf("configure media service: %w", err)
	}
	return service, nil
}
