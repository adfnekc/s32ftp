// Package config loads and validates the s32ftp configuration.
package config

import (
	"crypto/subtle"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// TLSMode defines how the FTP control channel is protected.
type TLSMode string

const (
	// TLSModeOff disables TLS entirely.
	TLSModeOff TLSMode = "off"
	// TLSModeExplicit enables AUTH TLS; clients may opt in.
	TLSModeExplicit TLSMode = "explicit"
	// TLSModeRequired enables AUTH TLS and refuses plaintext logins.
	TLSModeRequired TLSMode = "required"
	// TLSModeImplicit wraps the control connection in TLS from the first byte
	// (conventionally on port 990).
	TLSModeImplicit TLSMode = "implicit"
)

// Config is the root configuration document.
type Config struct {
	FTP     FTPConfig     `yaml:"ftp"`
	S3      S3Config      `yaml:"s3"`
	Logging LoggingConfig `yaml:"logging"`
}

// FTPConfig configures the front facing FTP server.
type FTPConfig struct {
	ListenIP                 string    `yaml:"listen_ip"`
	Port                     int       `yaml:"port"`
	Banner                   string    `yaml:"banner"`
	IdleTimeoutSeconds       int       `yaml:"idle_timeout_seconds"`
	ConnectionTimeoutSeconds int       `yaml:"connection_timeout_seconds"`
	PublicHost               string    `yaml:"public_host"`
	PassivePortRange         string    `yaml:"passive_port_range"`
	DisableActiveMode        bool      `yaml:"disable_active_mode"`
	DisableIPMatch           bool      `yaml:"disable_ip_match"`
	MaxClients               int       `yaml:"max_clients"`
	TLS                      FTPTLS    `yaml:"tls"`
	Users                    []FTPUser `yaml:"users"`
}

// FTPTLS configures transport security for the FTP server.
type FTPTLS struct {
	Mode       TLSMode `yaml:"mode"`
	CertFile   string  `yaml:"cert_file"`
	KeyFile    string  `yaml:"key_file"`
	MinVersion string  `yaml:"min_version"`
}

// FTPUser is a single FTP account.
type FTPUser struct {
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	PasswordHash string `yaml:"password_hash"`
	ReadOnly     bool   `yaml:"read_only"`
	Bucket       string `yaml:"bucket"`
	RootPrefix   string `yaml:"root_prefix"`
}

// S3Config configures the S3 backend.
type S3Config struct {
	EndpointURL           string `yaml:"endpoint_url"`
	Region                string `yaml:"region"`
	AccessKeyID           string `yaml:"access_key_id"`
	SecretAccessKey       string `yaml:"secret_access_key"`
	SessionToken          string `yaml:"session_token"`
	Bucket                string `yaml:"bucket"`
	RootPrefix            string `yaml:"root_prefix"`
	ForcePathStyle        bool   `yaml:"force_path_style"`
	InsecureSkipVerify    bool   `yaml:"insecure_skip_verify"`
	CABundle              string `yaml:"ca_bundle"`
	PartSizeMB            int    `yaml:"part_size_mb"`
	UploadConcurrency     int    `yaml:"upload_concurrency"`
	CreateBucketIfMissing bool   `yaml:"create_bucket_if_missing"`
	DirMarker             bool   `yaml:"dir_marker"`
}

// LoggingConfig configures logging.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

// Default returns a configuration with sensible defaults applied.
func Default() *Config {
	return &Config{
		FTP: FTPConfig{
			ListenIP:                 "0.0.0.0",
			Port:                     2121,
			Banner:                   "s32ftp FTP-to-S3 gateway",
			IdleTimeoutSeconds:       900,
			ConnectionTimeoutSeconds: 30,
			TLS: FTPTLS{
				Mode:       TLSModeOff,
				MinVersion: "1.2",
			},
		},
		S3: S3Config{
			Region:            "us-east-1",
			ForcePathStyle:    true,
			PartSizeMB:        8,
			UploadConcurrency: 4,
			DirMarker:         true,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
			Output: "stdout",
		},
	}
}

// Load reads a YAML file, applies defaults and environment overrides, then
// validates the result. An empty path loads defaults only.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config %q: %w", path, err)
		}

		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %q: %w", path, err)
		}
	}

	cfg.normalize()
	if err := cfg.applyEnv(); err != nil {
		return nil, err
	}
	cfg.normalize()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) normalize() {
	c.FTP.TLS.Mode = TLSMode(strings.ToLower(strings.TrimSpace(string(c.FTP.TLS.Mode))))
	if c.FTP.TLS.Mode == "" {
		c.FTP.TLS.Mode = TLSModeOff
	}
	c.Logging.Level = strings.ToLower(strings.TrimSpace(c.Logging.Level))
	c.Logging.Format = strings.ToLower(strings.TrimSpace(c.Logging.Format))
	c.Logging.Output = strings.TrimSpace(c.Logging.Output)

	if c.S3.PartSizeMB == 0 {
		c.S3.PartSizeMB = 8
	}
	if c.S3.UploadConcurrency == 0 {
		c.S3.UploadConcurrency = 4
	}
	if c.S3.Region == "" {
		c.S3.Region = "us-east-1"
	}

	for i := range c.FTP.Users {
		u := &c.FTP.Users[i]
		u.Username = strings.TrimSpace(u.Username)
		u.RootPrefix = NormalizePrefix(u.RootPrefix)
	}
	c.S3.RootPrefix = NormalizePrefix(c.S3.RootPrefix)
}

// NormalizePrefix turns an arbitrary prefix into either "" or "a/b/".
func NormalizePrefix(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}

	return p + "/"
}

func (c *Config) applyEnv() error {
	setString("S32FTP_FTP_LISTEN_IP", &c.FTP.ListenIP)
	setInt("S32FTP_FTP_PORT", &c.FTP.Port)
	setString("S32FTP_FTP_BANNER", &c.FTP.Banner)
	setString("S32FTP_FTP_PUBLIC_HOST", &c.FTP.PublicHost)
	setString("S32FTP_FTP_PASSIVE_PORT_RANGE", &c.FTP.PassivePortRange)
	setInt("S32FTP_FTP_MAX_CLIENTS", &c.FTP.MaxClients)
	setString("S32FTP_FTP_TLS_MODE", (*string)(&c.FTP.TLS.Mode))
	setString("S32FTP_FTP_TLS_CERT_FILE", &c.FTP.TLS.CertFile)
	setString("S32FTP_FTP_TLS_KEY_FILE", &c.FTP.TLS.KeyFile)

	setString("S32FTP_S3_ENDPOINT_URL", &c.S3.EndpointURL)
	setString("S32FTP_S3_REGION", &c.S3.Region)
	setString("S32FTP_S3_ACCESS_KEY_ID", &c.S3.AccessKeyID)
	setString("S32FTP_S3_SECRET_ACCESS_KEY", &c.S3.SecretAccessKey)
	setString("S32FTP_S3_SESSION_TOKEN", &c.S3.SessionToken)
	setString("S32FTP_S3_BUCKET", &c.S3.Bucket)
	setString("S32FTP_S3_ROOT_PREFIX", &c.S3.RootPrefix)
	if err := setBool("S32FTP_S3_FORCE_PATH_STYLE", &c.S3.ForcePathStyle); err != nil {
		return err
	}
	if err := setBool("S32FTP_S3_INSECURE_SKIP_VERIFY", &c.S3.InsecureSkipVerify); err != nil {
		return err
	}
	setString("S32FTP_S3_CA_BUNDLE", &c.S3.CABundle)
	setInt("S32FTP_S3_PART_SIZE_MB", &c.S3.PartSizeMB)
	setInt("S32FTP_S3_UPLOAD_CONCURRENCY", &c.S3.UploadConcurrency)
	if err := setBool("S32FTP_S3_CREATE_BUCKET_IF_MISSING", &c.S3.CreateBucketIfMissing); err != nil {
		return err
	}
	if err := setBool("S32FTP_S3_DIR_MARKER", &c.S3.DirMarker); err != nil {
		return err
	}

	setString("S32FTP_LOG_LEVEL", &c.Logging.Level)
	setString("S32FTP_LOG_FORMAT", &c.Logging.Format)
	setString("S32FTP_LOG_OUTPUT", &c.Logging.Output)

	return nil
}

func setString(key string, dst *string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

func setInt(key string, dst *int) {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			*dst = n
		}
	}
}

func setBool(key string, dst *bool) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}

	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("env %s: %w", key, err)
	}
	*dst = b

	return nil
}

// Validate checks the configuration for consistency.
func (c *Config) Validate() error {
	if c.FTP.Port < 0 || c.FTP.Port > 65535 {
		return fmt.Errorf("ftp.port %d out of range", c.FTP.Port)
	}
	if c.FTP.ListenIP != "" && strings.Contains(c.FTP.ListenIP, ":") {
		return fmt.Errorf("ftp.listen_ip must not contain a port: %q", c.FTP.ListenIP)
	}
	if c.FTP.TLSEnabled() {
		if c.FTP.TLS.CertFile == "" || c.FTP.TLS.KeyFile == "" {
			return fmt.Errorf("ftp.tls.mode %q requires cert_file and key_file", c.FTP.TLS.Mode)
		}
	}
	switch c.FTP.TLS.Mode {
	case TLSModeOff, TLSModeExplicit, TLSModeRequired, TLSModeImplicit:
	default:
		return fmt.Errorf("ftp.tls.mode %q is invalid (off|explicit|required|implicit)", c.FTP.TLS.Mode)
	}
	if _, err := c.FTP.ParsePassivePortRange(); err != nil {
		return fmt.Errorf("ftp.passive_port_range: %w", err)
	}
	if c.S3.Bucket == "" {
		return fmt.Errorf("s3.bucket is required")
	}
	if (c.S3.AccessKeyID == "") != (c.S3.SecretAccessKey == "") {
		return fmt.Errorf("s3.access_key_id and s3.secret_access_key must both be set or both empty")
	}
	if c.S3.PartSizeMB < 5 {
		return fmt.Errorf("s3.part_size_mb must be >= 5, got %d", c.S3.PartSizeMB)
	}
	if c.S3.UploadConcurrency < 1 {
		return fmt.Errorf("s3.upload_concurrency must be >= 1")
	}
	if len(c.FTP.Users) == 0 {
		return fmt.Errorf("ftp.users must contain at least one account")
	}
	seen := make(map[string]struct{}, len(c.FTP.Users))
	for i, u := range c.FTP.Users {
		if u.Username == "" {
			return fmt.Errorf("ftp.users[%d].username is empty", i)
		}
		if _, dup := seen[u.Username]; dup {
			return fmt.Errorf("ftp.users[%d].username %q is duplicated", i, u.Username)
		}
		seen[u.Username] = struct{}{}
		if u.Password == "" && u.PasswordHash == "" {
			return fmt.Errorf("ftp.users[%d] (%s): password or password_hash is required", i, u.Username)
		}
	}
	switch c.Logging.Level {
	case "", "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("logging.level %q is invalid", c.Logging.Level)
	}
	switch c.Logging.Format {
	case "", "text", "json":
	default:
		return fmt.Errorf("logging.format %q is invalid", c.Logging.Format)
	}

	return nil
}

// TLSEnabled reports whether the FTP server should load a certificate.
func (c *FTPConfig) TLSEnabled() bool {
	if c.TLS.Mode == "" || c.TLS.Mode == TLSModeOff {
		return c.TLS.CertFile != "" && c.TLS.KeyFile != ""
	}

	return true
}

// ParsePassivePortRange parses "start-end" into a pair. It returns nil when the
// range is unset.
func (c *FTPConfig) ParsePassivePortRange() (*[2]int, error) {
	raw := strings.TrimSpace(c.PassivePortRange)
	if raw == "" {
		return nil, nil
	}

	parts := strings.SplitN(raw, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("expected format start-end, got %q", raw)
	}

	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return nil, fmt.Errorf("invalid start port in %q", raw)
	}

	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil, fmt.Errorf("invalid end port in %q", raw)
	}

	if start < 1 || end > 65535 || start > end {
		return nil, fmt.Errorf("invalid port range %q", raw)
	}

	return &[2]int{start, end}, nil
}

// IdleTimeout returns the configured idle timeout as a duration.
func (c *FTPConfig) IdleTimeout() time.Duration {
	return time.Duration(c.IdleTimeoutSeconds) * time.Second
}

// ConnectionTimeout returns the data connection timeout as a duration.
func (c *FTPConfig) ConnectionTimeout() time.Duration {
	return time.Duration(c.ConnectionTimeoutSeconds) * time.Second
}

// LookupUser finds an account by name.
func (c *Config) LookupUser(username string) (*FTPUser, bool) {
	for i := range c.FTP.Users {
		if c.FTP.Users[i].Username == username {
			return &c.FTP.Users[i], true
		}
	}

	return nil, false
}

// CheckPassword verifies the password against the plaintext or bcrypt hash.
func (u *FTPUser) CheckPassword(password string) bool {
	if u.PasswordHash != "" {
		return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
	}

	return subtle.ConstantTimeCompare([]byte(u.Password), []byte(password)) == 1
}

// HashPassword produces a bcrypt hash suitable for ftp.users[].password_hash.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	return string(b), nil
}
