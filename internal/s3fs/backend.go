// Package s3fs implements the S3 backed filesystem that the FTP driver exposes
// to ftpserverlib's afero interface.
package s3fs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// ClientOptions configures the shared S3 client.
type ClientOptions struct {
	EndpointURL        string
	Region             string
	AccessKeyID        string
	SecretAccessKey    string
	SessionToken       string
	ForcePathStyle     bool
	InsecureSkipVerify bool
	CABundle           string
	PartSizeBytes      int64
	UploadConcurrency  int
}

// Client is a shared, concurrency-safe S3 client plus its multipart uploader.
type Client struct {
	s3       *s3.Client
	uploader *manager.Uploader
	logger   *slog.Logger
}

// NewClient builds an S3 client from the supplied options.
func NewClient(ctx context.Context, opts ClientOptions, logger *slog.Logger) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}

	httpClient, err := newHTTPClient(opts)
	if err != nil {
		return nil, err
	}

	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(opts.Region),
		awsconfig.WithHTTPClient(httpClient),
	}

	switch {
	case opts.AccessKeyID != "" && opts.SecretAccessKey != "":
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKeyID, opts.SecretAccessKey, opts.SessionToken),
		))
	case opts.SessionToken != "":
		return nil, errors.New("s3.session_token requires access_key_id and secret_access_key")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	clientFn := func(o *s3.Options) {
		o.UsePathStyle = opts.ForcePathStyle
		if opts.EndpointURL != "" {
			o.BaseEndpoint = aws.String(opts.EndpointURL)
		}
	}

	s3Client := s3.NewFromConfig(awsCfg, clientFn)

	partSize := opts.PartSizeBytes
	if partSize <= 0 {
		partSize = 8 * 1024 * 1024
	}
	if partSize < manager.MinUploadPartSize {
		return nil, fmt.Errorf("part size %d is below the S3 minimum of %d bytes", partSize, manager.MinUploadPartSize)
	}

	concurrency := opts.UploadConcurrency
	if concurrency < 1 {
		concurrency = 4
	}

	uploader := manager.NewUploader(s3Client, func(u *manager.Uploader) {
		u.PartSize = partSize
		u.Concurrency = concurrency
		u.LeavePartsOnError = false
	})

	return &Client{s3: s3Client, uploader: uploader, logger: logger}, nil
}

func newHTTPClient(opts ClientOptions) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}, nil
	}

	tr := transport.Clone()

	if opts.InsecureSkipVerify || opts.CABundle != "" {
		tlsCfg := &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: opts.InsecureSkipVerify, //nolint:gosec // explicit opt-in for self-signed endpoints
		}

		if opts.CABundle != "" {
			pem, err := os.ReadFile(opts.CABundle)
			if err != nil {
				return nil, fmt.Errorf("read ca_bundle %q: %w", opts.CABundle, err)
			}

			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("ca_bundle %q contains no usable certificates", opts.CABundle)
			}

			tlsCfg.RootCAs = pool
		}

		tr.TLSClientConfig = tlsCfg
	}

	// No global timeout: object transfers are long lived and bounded by the FTP
	// layer instead.
	return &http.Client{Transport: tr}, nil
}

// S3 exposes the underlying client for advanced operations (for example tests).
func (c *Client) S3() *s3.Client { return c.s3 }

// Logger exposes the configured logger.
func (c *Client) Logger() *slog.Logger { return c.logger }

// EnsureBucket creates the bucket when it does not exist yet.
func (c *Client) EnsureBucket(ctx context.Context, bucket string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return nil
	}

	if !isNotFound(err) {
		return fmt.Errorf("head bucket %q: %w", bucket, err)
	}

	if _, err := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return fmt.Errorf("create bucket %q: %w", bucket, err)
	}

	c.logger.Info("created S3 bucket", "bucket", bucket)

	return nil
}

// metaContext returns a context with the metadata operation timeout applied.
func metaContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 30*time.Second)
}

// isNotFound reports whether err is an S3 "missing object/bucket" error.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}

	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}

	var nf *s3types.NotFound
	if errors.As(err, &nf) {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchBucket", "404":
			return true
		}
	}

	return false
}

// isPreconditionFailed reports whether err is a 412/416 style error.
func isRangeNotSatisfiable(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "InvalidRange"
	}

	return false
}

// isRetryable reports whether an S3 error is transient and worth a 450 reply.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		if apiErr.ErrorFault() == smithy.FaultServer {
			return true
		}
	}

	return errors.Is(err, context.DeadlineExceeded)
}
