package s3fs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/afero"
)

// ErrReadOnly is returned for any mutating operation on a read-only view.
var ErrReadOnly = errors.New("account is read-only")

// FS is an afero filesystem view over a bucket/prefix of an S3 endpoint.
type FS struct {
	client     *Client
	bucket     string
	rootPrefix string
	dirMarker  bool
	readOnly   bool
	logger     *slog.Logger
}

// NewFS creates a filesystem view. The rootPrefix must already be normalized
// (either empty or terminated by "/").
func NewFS(client *Client, bucket, rootPrefix string, dirMarker, readOnly bool, logger *slog.Logger) *FS {
	if logger == nil {
		logger = slog.Default()
	}

	return &FS{
		client:     client,
		bucket:     bucket,
		rootPrefix: rootPrefix,
		dirMarker:  dirMarker,
		readOnly:   readOnly,
		logger:     logger,
	}
}

// Bucket returns the bucket backing this view.
func (f *FS) Bucket() string { return f.bucket }

// ReadOnly reports whether this view rejects mutations.
func (f *FS) ReadOnly() bool { return f.readOnly }

// Name implements afero.Fs.
func (f *FS) Name() string { return "s3fs" }

func (f *FS) key(p string) string { return joinRoot(f.rootPrefix, p) }

func (f *FS) keyDir(p string) string { return dirKey(f.rootPrefix, p) }

func (f *FS) readOnlyErr(op, name string) error {
	return &os.PathError{Op: op, Path: name, Err: ErrReadOnly}
}

func notExist(op, name string) error {
	return &os.PathError{Op: op, Path: name, Err: os.ErrNotExist}
}

func errno(op, name string, err error) error {
	return &os.PathError{Op: op, Path: name, Err: err}
}

// Stat implements afero.Fs.
func (f *FS) Stat(name string) (os.FileInfo, error) {
	clean := CleanFTPPath(name)
	if clean == "/" {
		return NewDirInfo("/"), nil
	}

	ctx, cancel := metaContext(context.Background())
	defer cancel()

	key := f.key(clean)
	if key == "" {
		return NewDirInfo("/"), nil
	}

	head, err := f.client.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return NewFileInfo(path.Base(clean), aws.ToInt64(head.ContentLength), aws.ToTime(head.LastModified)), nil
	}

	if !isNotFound(err) {
		return nil, fmt.Errorf("stat %s: %w", clean, err)
	}

	// Either a directory marker or a virtual directory implied by children.
	dirPrefix := f.keyDir(clean)

	list, listErr := f.client.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(f.bucket),
		Prefix:  aws.String(dirPrefix),
		MaxKeys: aws.Int32(1),
	})
	if listErr != nil {
		return nil, fmt.Errorf("stat %s: %w", clean, listErr)
	}

	if len(list.Contents) > 0 || len(list.CommonPrefixes) > 0 {
		return NewDirInfo(path.Base(clean)), nil
	}

	return nil, notExist("stat", clean)
}

// Open implements afero.Fs. Only directories may be opened through this path;
// file handles go through GetHandle.
func (f *FS) Open(name string) (afero.File, error) {
	info, err := f.Stat(name)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, errno("open", name, syscall.EISDIR)
	}

	return NewFile(f, CleanFTPPath(name), info), nil
}

// OpenFile implements afero.Fs. Write access is routed through the upload
// handle so that REST/APPE semantics are honoured.
func (f *FS) OpenFile(name string, flag int, _ os.FileMode) (afero.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_APPEND|os.O_TRUNC) != 0 {
		return f.GetHandle(name, flag, 0)
	}

	return f.Open(name)
}

// Create implements afero.Fs.
func (f *FS) Create(name string) (afero.File, error) {
	return f.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}

// GetHandle returns a streaming handle for the requested path. It is consumed
// by the FTP layer as a ftpserver.FileTransfer.
func (f *FS) GetHandle(name string, flags int, offset int64) (afero.File, error) {
	clean := CleanFTPPath(name)
	if clean == "/" {
		return nil, errno("open", clean, syscall.EISDIR)
	}

	if flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_APPEND|os.O_TRUNC) != 0 {
		if f.readOnly {
			return nil, f.readOnlyErr("open", clean)
		}

		return f.openUpload(clean, flags, offset)
	}

	return f.openDownload(clean, offset)
}

// Chmod is a no-op: S3 objects have no POSIX permission bits.
func (f *FS) Chmod(string, os.FileMode) error { return nil }

// Chown is a no-op: S3 objects have no POSIX ownership.
func (f *FS) Chown(string, int, int) error { return nil }

// Chtimes is a no-op: object modification time is managed by S3.
func (f *FS) Chtimes(string, time.Time, time.Time) error { return nil }

// getObject opens a ranged read of an object.
func (f *FS) getObject(ctx context.Context, key string, offset int64) (*s3.GetObjectOutput, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(key),
	}

	if offset > 0 {
		input.Range = aws.String(fmt.Sprintf("bytes=%d-", offset))
	}

	return f.client.s3.GetObject(ctx, input)
}

// headObject returns object metadata, or ok=false when the object is missing.
func (f *FS) headObject(ctx context.Context, key string) (*s3.HeadObjectOutput, bool, error) {
	out, err := f.client.s3.HeadObject(ctx, headObjectInput(f.bucket, key))
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}

		return nil, false, err
	}

	return out, true, nil
}

// headSize returns the object size, or ok=false when the object is missing.
func (f *FS) headSize(ctx context.Context, key string) (int64, bool, error) {
	out, found, err := f.headObject(ctx, key)
	if err != nil || !found {
		return 0, false, err
	}

	return sizeOf(out.ContentLength), true, nil
}

// prefixHasObjects reports whether any object exists under prefix.
func (f *FS) prefixHasObjects(ctx context.Context, prefix string) (bool, error) {
	out, err := f.client.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(f.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(1),
	})
	if err != nil {
		return false, err
	}

	return len(out.Contents) > 0, nil
}

var _ afero.Fs = (*FS)(nil)
