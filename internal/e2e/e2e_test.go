package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/jlaffaye/ftp"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/stretchr/testify/require"

	"github.com/adfnekc/s32ftp/internal/config"
	"github.com/adfnekc/s32ftp/internal/ftpdriver"
	"github.com/adfnekc/s32ftp/internal/s3fs"
)

type harness struct {
	addr string
	s3   *s3mem.Backend
}

type harnessOptions struct {
	s3Extra    string
	tls        bool
	withReadOn bool
}

func newHarness(t *testing.T, opts harnessOptions) *harness {
	t.Helper()

	backend := s3mem.New()
	faker := gofakes3.New(backend, gofakes3.WithAutoBucket(true))
	s3Server := httptest.NewServer(faker.Server())
	t.Cleanup(s3Server.Close)

	tlsBlock := ""
	if opts.tls {
		certFile, keyFile := writeSelfSignedCert(t)
		tlsBlock = fmt.Sprintf("  tls:\n    mode: explicit\n    cert_file: %q\n    key_file: %q\n", certFile, keyFile)
	}

	readOnlyUser := ""
	if opts.withReadOn {
		readOnlyUser = "    - username: ro\n      password: ro\n      read_only: true\n"
	}

	yaml := fmt.Sprintf(`
ftp:
  listen_ip: "127.0.0.1"
  port: 0
%[1]s  users:
    - username: admin
      password: secret
%[2]s
s3:
%[3]s  endpoint_url: %[4]q
  region: us-east-1
  access_key_id: test
  secret_access_key: test
  bucket: ftp
  force_path_style: true
  part_size_mb: 5
  upload_concurrency: 2
logging:
  level: error
  output: stdout
`, tlsBlock, readOnlyUser, opts.s3Extra, s3Server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(yaml), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	client, err := s3fs.NewClient(context.Background(), s3fs.ClientOptions{
		EndpointURL:        cfg.S3.EndpointURL,
		Region:             cfg.S3.Region,
		AccessKeyID:        cfg.S3.AccessKeyID,
		SecretAccessKey:    cfg.S3.SecretAccessKey,
		ForcePathStyle:     cfg.S3.ForcePathStyle,
		PartSizeBytes:      int64(cfg.S3.PartSizeMB) * 1024 * 1024,
		UploadConcurrency:  cfg.S3.UploadConcurrency,
		InsecureSkipVerify: true,
	}, logger)
	require.NoError(t, err)

	driver, err := ftpdriver.New(cfg, client, logger)
	require.NoError(t, err)

	server := ftpserver.NewFtpServer(driver)
	server.Logger = logger
	require.NoError(t, server.Listen())

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve() }()

	t.Cleanup(func() {
		_ = server.Stop()

		select {
		case <-serveErr:
		case <-time.After(5 * time.Second):
		}
	})

	return &harness{addr: server.Addr(), s3: backend}
}

func dial(t *testing.T, addr string, tlsExplicit bool) *ftp.ServerConn {
	t.Helper()

	options := []ftp.DialOption{ftp.DialWithTimeout(10 * time.Second)}
	if tlsExplicit {
		options = append(options, ftp.DialWithExplicitTLS(&tls.Config{InsecureSkipVerify: true})) //nolint:gosec
	}

	conn, err := ftp.Dial(addr, options...)
	require.NoError(t, err)
	require.NoError(t, conn.Login("admin", "secret"))

	t.Cleanup(func() { _ = conn.Quit() })

	return conn
}

func mustRead(t *testing.T, r io.Reader) string {
	t.Helper()

	data, err := io.ReadAll(r)
	require.NoError(t, err)

	return string(data)
}

func TestUploadDownloadListDelete(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := dial(t, h.addr, false)

	require.NoError(t, c.Stor("hello.txt", bytes.NewReader([]byte("hello world"))))

	entries, err := c.List("/")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "hello.txt", entries[0].Name)
	require.Equal(t, uint64(11), entries[0].Size)
	require.Equal(t, ftp.EntryTypeFile, entries[0].Type)

	size, err := c.FileSize("hello.txt")
	require.NoError(t, err)
	require.Equal(t, int64(11), size)

	resp, err := c.Retr("hello.txt")
	require.NoError(t, err)
	require.Equal(t, "hello world", mustRead(t, resp))
	require.NoError(t, resp.Close())

	require.NoError(t, c.Rename("hello.txt", "renamed.txt"))

	entry, err := c.GetEntry("renamed.txt")
	require.NoError(t, err)
	require.Equal(t, "renamed.txt", entry.Name)

	require.NoError(t, c.Delete("renamed.txt"))

	entries, err = c.List("/")
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestDirectories(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := dial(t, h.addr, false)

	require.NoError(t, c.MakeDir("dir1"))
	require.NoError(t, c.ChangeDir("dir1"))

	cwd, err := c.CurrentDir()
	require.NoError(t, err)
	require.Equal(t, "/dir1", cwd)

	require.NoError(t, c.Stor("nested.txt", bytes.NewReader([]byte("nested"))))

	require.NoError(t, c.ChangeDirToParent())

	entries, err := c.List("/")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "dir1", entries[0].Name)
	require.Equal(t, ftp.EntryTypeFolder, entries[0].Type)

	entries, err = c.List("/dir1")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "nested.txt", entries[0].Name)

	// Removing a non empty directory must fail.
	require.Error(t, c.RemoveDir("/dir1"))

	require.NoError(t, c.Delete("/dir1/nested.txt"))
	require.NoError(t, c.RemoveDir("/dir1"))

	entries, err = c.List("/")
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestRenameDirectory(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := dial(t, h.addr, false)

	require.NoError(t, c.MakeDir("src"))
	require.NoError(t, c.Stor("src/a.txt", bytes.NewReader([]byte("aaa"))))
	require.NoError(t, c.Stor("src/b.txt", bytes.NewReader([]byte("bbb"))))

	require.NoError(t, c.Rename("src", "dst"))

	entries, err := c.List("/dst")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	resp, err := c.Retr("dst/a.txt")
	require.NoError(t, err)
	require.Equal(t, "aaa", mustRead(t, resp))
	require.NoError(t, resp.Close())

	_, err = c.List("/src")
	require.Error(t, err)
}

func TestAppendAndResume(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := dial(t, h.addr, false)

	require.NoError(t, c.Stor("log.txt", bytes.NewReader([]byte("abc"))))
	require.NoError(t, c.Append("log.txt", bytes.NewReader([]byte("def"))))

	resp, err := c.Retr("log.txt")
	require.NoError(t, err)
	require.Equal(t, "abcdef", mustRead(t, resp))
	require.NoError(t, resp.Close())

	// Resume a download from an offset.
	resp, err = c.RetrFrom("log.txt", 3)
	require.NoError(t, err)
	require.Equal(t, "def", mustRead(t, resp))
	require.NoError(t, resp.Close())

	// Resume an upload at an offset: existing[0:3] + new data.
	require.NoError(t, c.StorFrom("log.txt", bytes.NewReader([]byte("XYZ")), 3))

	resp, err = c.Retr("log.txt")
	require.NoError(t, err)
	require.Equal(t, "abcXYZ", mustRead(t, resp))
	require.NoError(t, resp.Close())
}

func TestOverwriteFile(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := dial(t, h.addr, false)

	require.NoError(t, c.Stor("data.bin", bytes.NewReader([]byte("first version"))))
	require.NoError(t, c.Stor("data.bin", bytes.NewReader([]byte("second"))))

	resp, err := c.Retr("data.bin")
	require.NoError(t, err)
	require.Equal(t, "second", mustRead(t, resp))
	require.NoError(t, resp.Close())
}

func TestLargeMultipartTransfer(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := dial(t, h.addr, false)

	// 12 MiB with a 5 MiB part size forces a real multipart upload.
	const size = 12*1024*1024 + 12345
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	require.NoError(t, c.Stor("big.bin", bytes.NewReader(payload)))

	entry, err := c.GetEntry("big.bin")
	require.NoError(t, err)
	require.Equal(t, uint64(size), entry.Size)

	resp, err := c.Retr("big.bin")
	require.NoError(t, err)
	got, err := io.ReadAll(resp)
	require.NoError(t, err)
	require.NoError(t, resp.Close())

	require.Len(t, got, size)
	require.True(t, bytes.Equal(payload, got), "downloaded payload differs")

	// Resuming from inside the second part must work too.
	resp, err = c.RetrFrom("big.bin", uint64(size-1000))
	require.NoError(t, err)
	tail, err := io.ReadAll(resp)
	require.NoError(t, err)
	require.NoError(t, resp.Close())
	require.True(t, bytes.Equal(payload[size-1000:], tail))
}

func TestEmptyFiles(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := dial(t, h.addr, false)

	require.NoError(t, c.Stor("empty.txt", bytes.NewReader(nil)))

	entry, err := c.GetEntry("empty.txt")
	require.NoError(t, err)
	require.Equal(t, uint64(0), entry.Size)

	resp, err := c.Retr("empty.txt")
	require.NoError(t, err)
	require.Equal(t, "", mustRead(t, resp))
	require.NoError(t, resp.Close())
}

func TestReadOnlyUser(t *testing.T) {
	h := newHarness(t, harnessOptions{withReadOn: true})

	c, err := ftp.Dial(h.addr, ftp.DialWithTimeout(10*time.Second))
	require.NoError(t, err)
	require.NoError(t, c.Login("ro", "ro"))
	t.Cleanup(func() { _ = c.Quit() })

	require.Error(t, c.Stor("nope.txt", bytes.NewReader([]byte("x"))))
	require.Error(t, c.MakeDir("dir"))
}

func TestExplicitTLS(t *testing.T) {
	h := newHarness(t, harnessOptions{tls: true})
	c := dial(t, h.addr, true)

	require.NoError(t, c.Stor("tls.txt", bytes.NewReader([]byte("secure"))))

	resp, err := c.Retr("tls.txt")
	require.NoError(t, err)
	require.Equal(t, "secure", mustRead(t, resp))
	require.NoError(t, resp.Close())
}

func TestRootPrefixIsolation(t *testing.T) {
	h := newHarness(t, harnessOptions{s3Extra: "  root_prefix: \"data\"\n"})
	c := dial(t, h.addr, false)

	require.NoError(t, c.Stor("inside.txt", bytes.NewReader([]byte("x"))))

	names, err := c.NameList("/")
	require.NoError(t, err)
	require.Equal(t, []string{"inside.txt"}, names)

	// The object must live under the configured prefix, not at the bucket root.
	_, err = h.s3.GetObject("ftp", "data/inside.txt", nil)
	require.NoError(t, err)

	_, err = h.s3.GetObject("ftp", "inside.txt", nil)
	require.Error(t, err)
}

func writeSelfSignedCert(t *testing.T) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "s32ftp-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: der,
	}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{
		Type: "EC PRIVATE KEY", Bytes: keyDER,
	}), 0o600))

	return certFile, keyFile
}
