package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/RecRivenVI/gallery/internal/bootstrap"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/observability"
	version "github.com/RecRivenVI/gallery/pkg/galleryversion"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Printf("%s %s\n", version.ServiceName, version.Version)
		return 0
	}
	logger := observability.NewLogger(os.Stderr, slog.LevelInfo)
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		logger.Error("configuration_failed", "error", err.Error())
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := bootstrap.Run(ctx, cfg, logger); err != nil {
		logger.Error("galleryd_failed", "error", err.Error())
		return 1
	}
	return 0
}
