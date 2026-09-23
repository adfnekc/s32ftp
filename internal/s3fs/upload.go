package s3fs

import (
	"context"
	"io"
	"os"
	"path"
	"sync"
	"syscall"

	"github.com/spf13/afero"
)

// uploadFile streams data from the FTP client into an S3 object using a
// multipart upload driven by a pipe, so memory stays bounded by the part size.
type uploadFile struct {
	fs          *FS
	ctx         context.Context
	cancel      context.CancelFunc
	name        string
	key         string
	contentType string
	prefixLen   int64

	pr   *io.PipeReader
	pw   *io.PipeWriter
	done chan struct{}

	errMu   sync.Mutex
	err     error
	closed  bool
	pos     int64
	written int64
}

func (f *FS) openUpload(clean string, flags int, offset int64) (afero.File, error) {
	ctx, cancel := context.WithCancel(context.Background())

	var prefixLen int64

	if flags&os.O_TRUNC == 0 {
		switch {
		case flags&os.O_APPEND != 0:
			size, found, err := f.headSize(ctx, f.key(clean))
			if err != nil {
				cancel()

				return nil, err
			}
			if found {
				prefixLen = size
			}
		case offset > 0:
			size, found, err := f.headSize(ctx, f.key(clean))
			if err != nil {
				cancel()

				return nil, err
			}
			if !found {
				cancel()

				return nil, notExist("open", clean)
			}

			prefixLen = min(offset, size)
		}
	}

	pr, pw := io.Pipe()

	u := &uploadFile{
		fs:          f,
		ctx:         ctx,
		cancel:      cancel,
		name:        clean,
		key:         f.key(clean),
		contentType: objectContentType(f.key(clean)),
		prefixLen:   prefixLen,
		pr:          pr,
		pw:          pw,
		done:        make(chan struct{}),
		pos:         prefixLen,
	}

	go u.run()

	return u, nil
}

func (u *uploadFile) run() {
	defer close(u.done)

	body := io.Reader(u.pr)

	if u.prefixLen > 0 {
		out, err := u.fs.getObject(u.ctx, u.key, 0)
		if err != nil {
			u.fail(err)

			return
		}

		defer out.Body.Close()

		body = io.MultiReader(io.LimitReader(out.Body, u.prefixLen), u.pr)
	}

	_, err := u.fs.client.uploader.Upload(u.ctx, putObjectInput(u.fs.bucket, u.key, body, u.contentType))
	if err != nil {
		u.fail(err)
	}
}

func (u *uploadFile) fail(err error) {
	if err == nil {
		return
	}

	u.errMu.Lock()
	if u.err == nil {
		u.err = err
	}
	u.errMu.Unlock()

	_ = u.pr.CloseWithError(err)
}

func (u *uploadFile) recordedErr() error {
	u.errMu.Lock()
	defer u.errMu.Unlock()

	return u.err
}

// Write implements afero.File.
func (u *uploadFile) Write(p []byte) (int, error) {
	if u.closed {
		return 0, os.ErrClosed
	}

	n, err := u.pw.Write(p)
	u.pos += int64(n)
	u.written += int64(n)

	if err != nil {
		u.fail(err)
	}

	return n, err
}

// WriteAt implements afero.File. S3 uploads are sequential only.
func (u *uploadFile) WriteAt([]byte, int64) (int, error) {
	return 0, errno("write", u.name, syscall.EINVAL)
}

// WriteString implements afero.File.
func (u *uploadFile) WriteString(s string) (int, error) { return u.Write([]byte(s)) }

// Read implements afero.File.
func (u *uploadFile) Read([]byte) (int, error) { return 0, syscall.EBADF }

// ReadAt implements afero.File.
func (u *uploadFile) ReadAt([]byte, int64) (int, error) { return 0, syscall.EBADF }

// Seek implements afero.File. Only the resume position reported by GetHandle
// is accepted, and only before any byte has been written.
func (u *uploadFile) Seek(offset int64, whence int) (int64, error) {
	if u.closed {
		return 0, os.ErrClosed
	}
	if u.written > 0 {
		return 0, errno("seek", u.name, syscall.EINVAL)
	}

	var target int64

	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = u.pos + offset
	default:
		return 0, errno("seek", u.name, syscall.EINVAL)
	}

	if target != u.prefixLen {
		return 0, errno("seek", u.name, syscall.EINVAL)
	}

	u.pos = target

	return target, nil
}

// Name implements afero.File.
func (u *uploadFile) Name() string { return path.Base(u.name) }

// Readdir implements afero.File.
func (u *uploadFile) Readdir(int) ([]os.FileInfo, error) {
	return nil, errno("readdir", u.name, syscall.ENOTDIR)
}

// Readdirnames implements afero.File.
func (u *uploadFile) Readdirnames(int) ([]string, error) {
	return nil, errno("readdir", u.name, syscall.ENOTDIR)
}

// Stat implements afero.File.
func (u *uploadFile) Stat() (os.FileInfo, error) {
	info, err := u.fs.Stat(u.name)
	if err != nil {
		return NewFileInfo(path.Base(u.name), u.prefixLen, osTimeNow()), nil
	}

	return info, nil
}

// Sync implements afero.File.
func (u *uploadFile) Sync() error { return nil }

// Truncate implements afero.File.
func (u *uploadFile) Truncate(int64) error { return errno("truncate", u.name, syscall.EINVAL) }

// TransferError records an aborted transfer so the multipart upload is
// cancelled instead of being finalised with partial data.
func (u *uploadFile) TransferError(err error) {
	if err == nil {
		return
	}

	u.errMu.Lock()
	if u.err == nil {
		u.err = err
	}
	u.errMu.Unlock()

	_ = u.pr.CloseWithError(err)
	_ = u.pw.CloseWithError(err)
}

// Close finalises the upload and returns the resulting error, if any.
func (u *uploadFile) Close() error {
	if u.closed {
		return u.recordedErr()
	}

	u.closed = true

	if err := u.recordedErr(); err != nil {
		_ = u.pw.CloseWithError(err)
	} else {
		_ = u.pw.Close()
	}

	<-u.done
	u.cancel()

	return u.recordedErr()
}

var _ interface {
	TransferError(error)
} = (*uploadFile)(nil)
