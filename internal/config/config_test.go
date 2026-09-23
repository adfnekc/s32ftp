package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

const minimalConfig = `
ftp:
  users:
    - username: admin
      password: secret
s3:
  bucket: ftp
`

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig))
	require.NoError(t, err)

	require.Equal(t, "0.0.0.0", cfg.FTP.ListenIP)
	require.Equal(t, 2121, cfg.FTP.Port)
	require.Equal(t, TLSModeOff, cfg.FTP.TLS.Mode)
	require.Equal(t, 900, cfg.FTP.IdleTimeoutSeconds)
	require.Equal(t, "us-east-1", cfg.S3.Region)
	require.True(t, cfg.S3.ForcePathStyle)
	require.True(t, cfg.S3.DirMarker)
	require.Equal(t, 8, cfg.S3.PartSizeMB)
	require.Equal(t, 4, cfg.S3.UploadConcurrency)
	require.Equal(t, "info", cfg.Logging.Level)
}

func TestLoadAppliesEnvOverrides(t *testing.T) {
	t.Setenv("S32FTP_FTP_PORT", "9999")
	t.Setenv("S32FTP_S3_BUCKET", "from-env")
	t.Setenv("S32FTP_S3_ACCESS_KEY_ID", "ak")
	t.Setenv("S32FTP_S3_SECRET_ACCESS_KEY", "sk")
	t.Setenv("S32FTP_S3_FORCE_PATH_STYLE", "false")

	cfg, err := Load(writeConfig(t, minimalConfig))
	require.NoError(t, err)

	require.Equal(t, 9999, cfg.FTP.Port)
	require.Equal(t, "from-env", cfg.S3.Bucket)
	require.Equal(t, "ak", cfg.S3.AccessKeyID)
	require.False(t, cfg.S3.ForcePathStyle)
}

func TestValidateRejectsBadConfig(t *testing.T) {
	cases := map[string]string{
		"no bucket": `
ftp:
  users: [{username: a, password: b}]
s3: {}
`,
		"no users": `
ftp:
  users: []
s3:
  bucket: ftp
`,
		"user without password": `
ftp:
  users: [{username: a}]
s3:
  bucket: ftp
`,
		"small part size": `
ftp:
  users: [{username: a, password: b}]
s3:
  bucket: ftp
  part_size_mb: 2
`,
		"tls without cert": `
ftp:
  tls:
    mode: explicit
  users: [{username: a, password: b}]
s3:
  bucket: ftp
`,
		"bad tls mode": `
ftp:
  tls:
    mode: maybe
  users: [{username: a, password: b}]
s3:
  bucket: ftp
`,
		"ak without sk": `
ftp:
  users: [{username: a, password: b}]
s3:
  bucket: ftp
  access_key_id: ak
`,
		"bad port range": `
ftp:
  passive_port_range: "60000-50000"
  users: [{username: a, password: b}]
s3:
  bucket: ftp
`,
		"duplicate users": `
ftp:
  users:
    - {username: a, password: b}
    - {username: a, password: c}
s3:
  bucket: ftp
`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, body))
			require.Error(t, err)
		})
	}
}

func TestCheckPassword(t *testing.T) {
	hash, err := HashPassword("s3cret")
	require.NoError(t, err)

	plain := &FTPUser{Password: "s3cret"}
	require.True(t, plain.CheckPassword("s3cret"))
	require.False(t, plain.CheckPassword("nope"))

	hashed := &FTPUser{PasswordHash: hash}
	require.True(t, hashed.CheckPassword("s3cret"))
	require.False(t, hashed.CheckPassword("nope"))
}

func TestNormalizePrefix(t *testing.T) {
	require.Equal(t, "", NormalizePrefix(""))
	require.Equal(t, "", NormalizePrefix("/"))
	require.Equal(t, "a/b/", NormalizePrefix("a/b"))
	require.Equal(t, "a/b/", NormalizePrefix("/a/b/"))
}

func TestParsePassivePortRange(t *testing.T) {
	cfg := &FTPConfig{PassivePortRange: "50000-50100"}

	r, err := cfg.ParsePassivePortRange()
	require.NoError(t, err)
	require.Equal(t, [2]int{50000, 50100}, *r)

	cfg.PassivePortRange = ""
	r, err = cfg.ParsePassivePortRange()
	require.NoError(t, err)
	require.Nil(t, r)

	cfg.PassivePortRange = "50000"
	_, err = cfg.ParsePassivePortRange()
	require.Error(t, err)
}
