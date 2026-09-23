package ftpdriver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"github.com/adfnekc/s32ftp/internal/config"
	"github.com/adfnekc/s32ftp/internal/s3fs"
	ftpserver "github.com/fclairamb/ftpserverlib"
)

// ErrTooManyClients is returned when the configured client limit is reached.
var ErrTooManyClients = errors.New("too many clients connected")

// ErrInvalidCredentials is returned on a failed login.
var ErrInvalidCredentials = errors.New("invalid username or password")

// ErrTLSNotConfigured is returned when a TLS config is requested but absent.
var ErrTLSNotConfigured = errors.New("TLS is not configured")

// Driver implements ftpserver.MainDriver.
type Driver struct {
	cfg    *config.Config
	client *s3fs.Client
	logger *slog.Logger

	tlsConfig *tls.Config

	mu      sync.Mutex
	clients map[uint32]struct{}
}

// New builds the FTP driver.
func New(cfg *config.Config, client *s3fs.Client, logger *slog.Logger) (*Driver, error) {
	if logger == nil {
		logger = slog.Default()
	}

	d := &Driver{
		cfg:     cfg,
		client:  client,
		logger:  logger,
		clients: make(map[uint32]struct{}),
	}

	if cfg.FTP.TLSEnabled() {
		tlsCfg, err := loadTLSConfig(cfg.FTP.TLS)
		if err != nil {
			return nil, err
		}

		d.tlsConfig = tlsCfg
	}

	return d, nil
}

// GetSettings implements ftpserver.MainDriver.
func (d *Driver) GetSettings() (*ftpserver.Settings, error) {
	cfg := d.cfg.FTP

	settings := &ftpserver.Settings{
		ListenAddr:             net.JoinHostPort(cfg.ListenIP, strconv.Itoa(cfg.Port)),
		Banner:                 cfg.Banner,
		IdleTimeout:            cfg.IdleTimeoutSeconds,
		ConnectionTimeout:      cfg.ConnectionTimeoutSeconds,
		PublicHost:             cfg.PublicHost,
		DisableActiveMode:      cfg.DisableActiveMode,
		DisableMFMT:            true,
		DefaultTransferType:    ftpserver.TransferTypeBinary,
		PasvConnectionsCheck:   ftpserver.IPMatchRequired,
		ActiveConnectionsCheck: ftpserver.IPMatchRequired,
	}

	if cfg.DisableIPMatch {
		settings.PasvConnectionsCheck = ftpserver.IPMatchDisabled
		settings.ActiveConnectionsCheck = ftpserver.IPMatchDisabled
	}

	if portRange, err := cfg.ParsePassivePortRange(); err != nil {
		return nil, err
	} else if portRange != nil {
		settings.PassiveTransferPortRange = ftpserver.PortRange{Start: portRange[0], End: portRange[1]}
	}

	switch cfg.TLS.Mode {
	case config.TLSModeImplicit:
		settings.TLSRequired = ftpserver.ImplicitEncryption
	case config.TLSModeRequired:
		settings.TLSRequired = ftpserver.MandatoryEncryption
	default:
		settings.TLSRequired = ftpserver.ClearOrEncrypted
	}

	return settings, nil
}

// ClientConnected implements ftpserver.MainDriver.
func (d *Driver) ClientConnected(cc ftpserver.ClientContext) (string, error) {
	d.mu.Lock()
	if max := d.cfg.FTP.MaxClients; max > 0 && len(d.clients) >= max {
		d.mu.Unlock()

		d.logger.Warn("rejecting FTP client: limit reached",
			"client", cc.RemoteAddr().String(), "limit", max)

		return "Too many clients connected", ErrTooManyClients
	}
	d.clients[cc.ID()] = struct{}{}
	d.mu.Unlock()

	if d.logger.Enabled(context.Background(), slog.LevelDebug) {
		cc.SetDebug(true)
	}

	d.logger.Info("FTP client connected",
		"client", cc.RemoteAddr().String(),
		"id", cc.ID())

	return d.cfg.FTP.Banner, nil
}

// ClientDisconnected implements ftpserver.MainDriver.
func (d *Driver) ClientDisconnected(cc ftpserver.ClientContext) {
	d.mu.Lock()
	delete(d.clients, cc.ID())
	d.mu.Unlock()

	d.logger.Info("FTP client disconnected",
		"client", cc.RemoteAddr().String(),
		"id", cc.ID())
}

// ActiveClients returns the current number of connected clients.
func (d *Driver) ActiveClients() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.clients)
}

// AuthUser implements ftpserver.MainDriver.
func (d *Driver) AuthUser(cc ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	account, ok := d.cfg.LookupUser(user)
	if !ok || !account.CheckPassword(pass) {
		d.logger.Warn("FTP authentication failed",
			"user", user,
			"client", cc.RemoteAddr().String())

		return nil, fmt.Errorf("%w: %s", ErrInvalidCredentials, user)
	}

	bucket := d.cfg.S3.Bucket
	if account.Bucket != "" {
		bucket = account.Bucket
	}

	rootPrefix := d.cfg.S3.RootPrefix
	if account.RootPrefix != "" {
		rootPrefix = account.RootPrefix
	}

	fs := s3fs.NewFS(d.client, bucket, rootPrefix, d.cfg.S3.DirMarker, account.ReadOnly, d.logger)

	d.logger.Info("FTP user authenticated",
		"user", user,
		"client", cc.RemoteAddr().String(),
		"bucket", bucket,
		"root_prefix", rootPrefix,
		"read_only", account.ReadOnly,
		"tls", cc.HasTLSForControl())

	return NewClient(fs, d.logger), nil
}

// GetTLSConfig implements ftpserver.MainDriver.
func (d *Driver) GetTLSConfig() (*tls.Config, error) {
	if d.tlsConfig == nil {
		return nil, ErrTLSNotConfigured
	}

	return d.tlsConfig, nil
}

var _ ftpserver.MainDriver = (*Driver)(nil)
