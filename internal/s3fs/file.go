package s3fs

import (
	"io"
	"mime"
	"os"
	"path"
	"strings"
	"syscall"
)

// File is a read-only afero.File handle for a directory.
type File struct {
	fs      *FS
	name    string
	info    os.FileInfo
	entries []os.FileInfo
	offset  int
}

// NewFile builds a directory handle.
func NewFile(fs *FS, name string, info os.FileInfo) *File {
	return &File{fs: fs, name: name, info: info}
}

// Name implements afero.File.
func (f *File) Name() string {
	if f.name == "/" {
		return "/"
	}

	return path.Base(f.name)
}

// Read implements afero.File.
func (f *File) Read([]byte) (int, error) { return 0, errno("read", f.name, syscall.EISDIR) }

// ReadAt implements afero.File.
func (f *File) ReadAt([]byte, int64) (int, error) {
	return 0, errno("read", f.name, syscall.EISDIR)
}

// Write implements afero.File.
func (f *File) Write([]byte) (int, error) { return 0, errno("write", f.name, syscall.EISDIR) }

// WriteAt implements afero.File.
func (f *File) WriteAt([]byte, int64) (int, error) {
	return 0, errno("write", f.name, syscall.EISDIR)
}

// WriteString implements afero.File.
func (f *File) WriteString(string) (int, error) {
	return 0, errno("write", f.name, syscall.EISDIR)
}

// Seek implements afero.File. Directory listing only supports seeking to zero.
func (f *File) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && (whence == io.SeekStart || whence == io.SeekCurrent) {
		f.offset = 0

		return 0, nil
	}

	return 0, errno("seek", f.name, syscall.EINVAL)
}

func (f *File) load() error {
	if f.entries != nil {
		return nil
	}

	entries, err := f.fs.ReadDir(f.name)
	if err != nil {
		return err
	}

	f.entries = entries

	return nil
}

// Readdir implements afero.File.
func (f *File) Readdir(count int) ([]os.FileInfo, error) {
	if err := f.load(); err != nil {
		return nil, err
	}

	if count <= 0 {
		out := f.entries[f.offset:]
		f.offset = len(f.entries)

		return out, nil
	}

	if f.offset >= len(f.entries) {
		return nil, io.EOF
	}

	end := min(f.offset+count, len(f.entries))
	out := f.entries[f.offset:end]
	f.offset = end

	return out, nil
}

// Readdirnames implements afero.File.
func (f *File) Readdirnames(count int) ([]string, error) {
	infos, err := f.Readdir(count)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name())
	}

	return names, nil
}

// Stat implements afero.File.
func (f *File) Stat() (os.FileInfo, error) { return f.info, nil }

// Sync implements afero.File.
func (f *File) Sync() error { return nil }

// Truncate implements afero.File.
func (f *File) Truncate(int64) error { return errno("truncate", f.name, syscall.EISDIR) }

// Close implements afero.File.
func (f *File) Close() error { return nil }

var _ io.ReaderAt = (*File)(nil)

// objectContentType guesses a content type from the object key.
func objectContentType(key string) string {
	ct := mime.TypeByExtension(path.Ext(strings.TrimSuffix(key, "/")))
	if ct == "" {
		return "application/octet-stream"
	}

	return ct
}
