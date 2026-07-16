package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/descriptor"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	fileSystem := filesystem.OS{}
	if err := cfg.AppDirs.ValidateDisjoint(fileSystem, cfg.SourceRoots); err != nil {
		return err
	}
	if err := cfg.AppDirs.Ensure(fileSystem); err != nil {
		return err
	}
	store, err := storage.Open(ctx, cfg.AppDirs)
	if err != nil {
		return err
	}
	defer store.Close()

	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("监听失败: %w", err)
	}
	defer listener.Close()

	runtimeDescriptor, err := descriptor.New(listener.Addr().String())
	if err != nil {
		return err
	}
	descriptorPath, err := descriptor.Publish(cfg.AppDirs.Runtime, runtimeDescriptor)
	if err != nil {
		return err
	}
	defer descriptor.RemoveIfOwned(descriptorPath, runtimeDescriptor.StartupNonce)

	systemClock := clock.System{}
	personal, err := auth.NewPersonal(store.Control.SQL(), systemClock, identity.NewGenerator(systemClock), nil)
	if err != nil {
		return err
	}
	resources, err := application.NewResources(store.Control.SQL(), cfg.AppDirs, fileSystem, systemClock, identity.NewGenerator(systemClock))
	if err != nil {
		return err
	}
	handler := httpapi.New(cfg.Mode, store, systemClock, personal, resources, logger)
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()
	logger.Info("galleryd_started", "address", listener.Addr().String(), "mode", cfg.Mode)

	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		logger.Info("galleryd_stopped")
		return nil
	case err := <-serveError:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
