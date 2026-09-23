// Package ftpdriver implements the ftpserverlib MainDriver and ClientDriver on
// top of the S3 filesystem.
package ftpdriver

import (
	"crypto/tls"
	"fmt"

	"github.com/adfnekc/s32ftp/internal/config"
)

func loadTLSConfig(cfg config.FTPTLS) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS key pair: %w", err)
	}

	minVersion, err := parseTLSVersion(cfg.MinVersion)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   minVersion,
	}, nil
}

func parseTLSVersion(v string) (uint16, error) {
	switch v {
	case "", "1.2", "1.2.0", "tls1.2":
		return tls.VersionTLS12, nil
	case "1.0", "tls1.0":
		return tls.VersionTLS10, nil
	case "1.1", "tls1.1":
		return tls.VersionTLS11, nil
	case "1.3", "tls1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unsupported ftp.tls.min_version %q", v)
	}
}
