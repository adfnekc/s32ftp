package s3fs

import (
	"os"
	"time"
)

// FileInfo implements os.FileInfo for S3 objects and virtual directories.
type FileInfo struct {
	NameValue    string
	SizeValue    int64
	ModeValue    os.FileMode
	ModTimeValue time.Time
	DirValue     bool
}

// NewDirInfo builds a FileInfo for a directory.
func NewDirInfo(name string) *FileInfo {
	return &FileInfo{
		NameValue: name,
		ModeValue: os.ModeDir | 0o755,
		DirValue:  true,
	}
}

// NewFileInfo builds a FileInfo for an object.
func NewFileInfo(name string, size int64, modTime time.Time) *FileInfo {
	return &FileInfo{
		NameValue:    name,
		SizeValue:    size,
		ModeValue:    0o644,
		ModTimeValue: modTime,
	}
}

// Name implements os.FileInfo.
func (f *FileInfo) Name() string { return f.NameValue }

// Size implements os.FileInfo.
func (f *FileInfo) Size() int64 { return f.SizeValue }

// Mode implements os.FileInfo.
func (f *FileInfo) Mode() os.FileMode { return f.ModeValue }

// ModTime implements os.FileInfo.
func (f *FileInfo) ModTime() time.Time { return f.ModTimeValue }

// IsDir implements os.FileInfo.
func (f *FileInfo) IsDir() bool { return f.DirValue }

// Sys implements os.FileInfo.
func (f *FileInfo) Sys() any { return nil }
