// Command s32ftp exposes an FTP(S) frontend backed by an S3 compatible object
// store.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/adfnekc/s32ftp/internal/config"
	"github.com/adfnekc/s32ftp/internal/ftpdriver"
	"github.com/adfnekc/s32ftp/internal/logging"
	"github.com/adfnekc/s32ftp/internal/s3fs"
)

// version is overridable at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath   string
		showVersion  bool
		hashPassword string
	)

	flag.StringVar(&configPath, "config", "", "path to the YAML configuration file")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&hashPassword, "hash-password", "", "print a bcrypt hash for the given password and exit")
	flag.Parse()

	if showVersion {
		fmt.Println("s32ftp", version)

		return nil
	}

	if hashPassword != "" {
		hash, err := config.HashPassword(hashPassword)
		if err != nil {
			return err
		}

		fmt.Println(hash)

		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	logger, logCloser, err := logging.New(cfg.Logging)
	if err != nil {
		return err
	}
	if logCloser != nil {
		defer func() { _ = logCloser.Close() }()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s3Client, err := s3fs.NewClient(ctx, s3fs.ClientOptions{
		EndpointURL:        cfg.S3.EndpointURL,
		Region:             cfg.S3.Region,
		AccessKeyID:        cfg.S3.AccessKeyID,
		SecretAccessKey:    cfg.S3.SecretAccessKey,
		SessionToken:       cfg.S3.SessionToken,
		ForcePathStyle:     cfg.S3.ForcePathStyle,
		InsecureSkipVerify: cfg.S3.InsecureSkipVerify,
		CABundle:           cfg.S3.CABundle,
		PartSizeBytes:      int64(cfg.S3.PartSizeMB) * 1024 * 1024,
		UploadConcurrency:  cfg.S3.UploadConcurrency,
	}, logger)
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}

	if cfg.S3.CreateBucketIfMissing {
		if err := s3Client.EnsureBucket(ctx, cfg.S3.Bucket); err != nil {
			return err
		}
	}

	driver, err := ftpdriver.New(cfg, s3Client, logger)
	if err != nil {
		return err
	}

	server := ftpserver.NewFtpServer(driver)
	server.Logger = logger

	if err := server.Listen(); err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	logger.Info("s32ftp started",
		"version", version,
		"listen", server.Addr(),
		"tls_mode", cfg.FTP.TLS.Mode,
		"bucket", cfg.S3.Bucket,
		"endpoint", cfg.S3.EndpointURL)

	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve()
		if err != nil && !errors.Is(err, io.EOF) {
			serveErr <- err

			return
		}

		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}

		return nil
	case <-ctx.Done():
		logger.Info("shutdown signal received, stopping FTP listener")
	}

	if err := server.Stop(); err != nil && !errors.Is(err, ftpserver.ErrNotListening) {
		logger.Warn("error while stopping FTP server", "err", err)
	}

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
	case <-time.After(10 * time.Second):
		logger.Warn("timed out waiting for in-flight transfers to finish")
	}

	logger.Info("s32ftp stopped")

	return nil
}
