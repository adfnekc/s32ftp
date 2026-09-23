package s3fs

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"syscall"
	"time"

	"github.com/spf13/afero"
)

// downloadFile streams an S3 object to the FTP client. It supports REST style
// seeks by reopening the object with a range request.
type downloadFile struct {
	fs     *FS
	ctx    context.Context
	cancel context.CancelFunc
	name   string
	key    string
	size   int64
	mod    time.Time
	pos    int64
	body   io.ReadCloser
	closed bool
}

func (f *FS) openDownload(clean string, offset int64) (afero.File, error) {
	ctx, cancel := context.WithCancel(context.Background())

	head, found, err := f.headObject(ctx, f.key(clean))
	if err != nil {
		cancel()

		return nil, err
	}

	if !found {
		cancel()

		return nil, notExist("open", clean)
	}

	size := sizeOf(head.ContentLength)

	if offset < 0 || offset > size {
		cancel()

		return nil, errno("open", clean, syscall.EINVAL)
	}

	return &downloadFile{
		fs:     f,
		ctx:    ctx,
		cancel: cancel,
		name:   clean,
		key:    f.key(clean),
		size:   size,
		mod:    timeOf(head.LastModified),
		pos:    offset,
	}, nil
}

func (d *downloadFile) ensureBody() error {
	if d.body != nil {
		return nil
	}

	if d.pos >= d.size {
		d.body = io.NopCloser(emptyReader{})

		return nil
	}

	out, err := d.fs.getObject(d.ctx, d.key, d.pos)
	if err != nil {
		return err
	}

	d.body = out.Body

	return nil
}

// Read implements afero.File.
func (d *downloadFile) Read(p []byte) (int, error) {
	if d.closed {
		return 0, os.ErrClosed
	}

	if err := d.ensureBody(); err != nil {
		return 0, err
	}

	n, err := d.body.Read(p)
	d.pos += int64(n)

	if errors.Is(err, io.EOF) {
		_ = d.body.Close()
		d.body = nil
	}

	return n, err
}

// ReadAt implements afero.File using an independent ranged request.
func (d *downloadFile) ReadAt(p []byte, off int64) (int, error) {
	if d.closed {
		return 0, os.ErrClosed
	}
	if off < 0 {
		return 0, syscall.EINVAL
	}
	if off >= d.size {
		return 0, io.EOF
	}

	out, err := d.fs.getObject(d.ctx, d.key, off)
	if err != nil {
		return 0, err
	}
	defer out.Body.Close()

	return io.ReadFull(out.Body, p)
}

// Write implements afero.File.
func (d *downloadFile) Write([]byte) (int, error) { return 0, syscall.EBADF }

// WriteAt implements afero.File.
func (d *downloadFile) WriteAt([]byte, int64) (int, error) { return 0, syscall.EBADF }

// WriteString implements afero.File.
func (d *downloadFile) WriteString(string) (int, error) { return 0, syscall.EBADF }

// Seek implements afero.File.
func (d *downloadFile) Seek(offset int64, whence int) (int64, error) {
	if d.closed {
		return 0, os.ErrClosed
	}

	var target int64

	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = d.pos + offset
	case io.SeekEnd:
		target = d.size + offset
	default:
		return 0, errno("seek", d.name, syscall.EINVAL)
	}

	if target < 0 {
		return 0, errno("seek", d.name, syscall.EINVAL)
	}

	if target != d.pos {
		if d.body != nil {
			_ = d.body.Close()
			d.body = nil
		}

		d.pos = target
	}

	return d.pos, nil
}

// Name implements afero.File.
func (d *downloadFile) Name() string {
	if d.name == "/" {
		return "/"
	}

	return path.Base(d.name)
}

// Readdir implements afero.File.
func (d *downloadFile) Readdir(int) ([]os.FileInfo, error) {
	return nil, errno("readdir", d.name, syscall.ENOTDIR)
}

// Readdirnames implements afero.File.
func (d *downloadFile) Readdirnames(int) ([]string, error) {
	return nil, errno("readdir", d.name, syscall.ENOTDIR)
}

// Stat implements afero.File.
func (d *downloadFile) Stat() (os.FileInfo, error) {
	return NewFileInfo(path.Base(d.name), d.size, d.mod), nil
}

// Sync implements afero.File.
func (d *downloadFile) Sync() error { return nil }

// Truncate implements afero.File.
func (d *downloadFile) Truncate(int64) error { return syscall.EBADF }

// Close implements afero.File.
func (d *downloadFile) Close() error {
	if d.closed {
		return nil
	}

	d.closed = true
	d.cancel()

	if d.body != nil {
		err := d.body.Close()
		d.body = nil

		return err
	}

	return nil
}

// emptyReader is always at EOF.
type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

var _ os.FileInfo = (*FileInfo)(nil)
