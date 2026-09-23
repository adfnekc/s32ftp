package s3fs

import (
	"context"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ReadDir implements the ClientDriverExtensionFileList extension.
func (f *FS) ReadDir(name string) ([]os.FileInfo, error) {
	clean := CleanFTPPath(name)

	if _, err := f.Stat(clean); err != nil {
		return nil, err
	}

	ctx, cancel := metaContext(context.Background())
	defer cancel()

	prefix := f.keyDir(clean)

	var (
		infos []os.FileInfo
		seen  = make(map[string]struct{})
		token *string
	)

	add := func(info os.FileInfo) {
		if _, ok := seen[info.Name()]; ok {
			// A file and a directory can share a name in S3 (keys "dir" and
			// "dir/"). Keep the first entry so LIST output stays unique.
			return
		}

		seen[info.Name()] = struct{}{}
		infos = append(infos, info)
	}

	for {
		out, err := f.client.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(f.bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}

		for _, cp := range out.CommonPrefixes {
			cpPrefix := aws.ToString(cp.Prefix)
			entry := relativeName(prefix, cpPrefix)
			if entry == "" {
				continue
			}

			add(NewDirInfo(entry))
		}

		for i := range out.Contents {
			obj := out.Contents[i]
			key := aws.ToString(obj.Key)

			if key == prefix || strings.HasSuffix(key, "/") {
				continue
			}

			entry := relativeName(prefix, key)
			if entry == "" || strings.Contains(entry, "/") {
				continue
			}

			add(NewFileInfo(entry, aws.ToInt64(obj.Size), aws.ToTime(obj.LastModified)))
		}

		if !aws.ToBool(out.IsTruncated) {
			break
		}

		token = out.NextContinuationToken
	}

	sort.Slice(infos, func(i, j int) bool {
		if infos[i].IsDir() != infos[j].IsDir() {
			return infos[i].IsDir()
		}

		return infos[i].Name() < infos[j].Name()
	})

	return infos, nil
}

// Mkdir implements afero.Fs.
func (f *FS) Mkdir(name string, _ os.FileMode) error {
	if f.readOnly {
		return f.readOnlyErr("mkdir", name)
	}

	clean := CleanFTPPath(name)
	if clean == "/" {
		return nil
	}

	ctx, cancel := metaContext(context.Background())
	defer cancel()

	// If a file with the same key already exists the directory would be
	// shadowed, so refuse the operation like POSIX mkdir would.
	key := f.key(clean)
	if _, found, err := f.headSize(ctx, key); err != nil {
		return err
	} else if found {
		return errno("mkdir", clean, syscall.EEXIST)
	}

	if exists, err := f.prefixHasObjects(ctx, f.keyDir(clean)); err != nil {
		return err
	} else if exists {
		return errno("mkdir", clean, syscall.EEXIST)
	}

	if !f.dirMarker {
		return nil
	}

	if _, err := f.client.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(f.bucket),
		Key:           aws.String(f.keyDir(clean)),
		Body:          strings.NewReader(""),
		ContentLength: aws.Int64(0),
	}); err != nil {
		return err
	}

	return nil
}

// MkdirAll implements afero.Fs. Ancestor directories are implied by the
// prefix, so only the leaf marker is written.
func (f *FS) MkdirAll(name string, perm os.FileMode) error {
	if f.readOnly {
		return f.readOnlyErr("mkdir", name)
	}

	clean := CleanFTPPath(name)
	if clean == "/" {
		return nil
	}

	if _, err := f.Stat(clean); err == nil {
		return nil
	}

	return f.Mkdir(clean, perm)
}

// Remove implements afero.Fs: it removes a file, or an empty directory.
func (f *FS) Remove(name string) error {
	if f.readOnly {
		return f.readOnlyErr("remove", name)
	}

	clean := CleanFTPPath(name)
	if clean == "/" {
		return errno("remove", clean, syscall.EACCES)
	}

	info, err := f.Stat(clean)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return f.RemoveDir(clean)
	}

	ctx, cancel := metaContext(context.Background())
	defer cancel()

	if _, err := f.client.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(f.key(clean)),
	}); err != nil {
		return err
	}

	f.logger.Debug("deleted S3 object", "bucket", f.bucket, "key", f.key(clean))

	return nil
}

// RemoveDir implements the ClientDriverExtensionRemoveDir extension.
func (f *FS) RemoveDir(name string) error {
	if f.readOnly {
		return f.readOnlyErr("rmdir", name)
	}

	clean := CleanFTPPath(name)
	if clean == "/" {
		return errno("rmdir", clean, syscall.EACCES)
	}

	info, err := f.Stat(clean)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return errno("rmdir", clean, syscall.ENOTDIR)
	}

	ctx, cancel := metaContext(context.Background())
	defer cancel()

	prefix := f.keyDir(clean)

	out, err := f.client.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(f.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(2),
	})
	if err != nil {
		return err
	}

	for i := range out.Contents {
		if aws.ToString(out.Contents[i].Key) != prefix {
			return errno("rmdir", clean, syscall.ENOTEMPTY)
		}
	}

	if _, err := f.client.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(prefix),
	}); err != nil {
		return err
	}

	return nil
}

// RemoveAll implements afero.Fs: it removes a path and everything below it.
func (f *FS) RemoveAll(name string) error {
	if f.readOnly {
		return f.readOnlyErr("remove", name)
	}

	clean := CleanFTPPath(name)
	if clean == "/" {
		return errno("remove", clean, syscall.EACCES)
	}

	ctx, cancel := metaContext(context.Background())
	defer cancel()

	info, statErr := f.Stat(clean)
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}

	if statErr == nil && !info.IsDir() {
		return f.removeKey(ctx, f.key(clean))
	}

	keys, err := f.listAllKeys(ctx, f.keyDir(clean))
	if err != nil {
		return err
	}

	if err := f.deleteKeys(ctx, keys); err != nil {
		return err
	}

	if statErr == nil {
		if err := f.removeKey(ctx, f.keyDir(clean)); err != nil {
			return err
		}
	}

	return nil
}

func (f *FS) removeKey(ctx context.Context, key string) error {
	_, err := f.client.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(key),
	})

	return err
}

// Rename implements afero.Fs using copy-plus-delete, recursively for prefixes.
func (f *FS) Rename(oldname, newname string) error {
	if f.readOnly {
		return f.readOnlyErr("rename", oldname)
	}

	oldClean := CleanFTPPath(oldname)
	newClean := CleanFTPPath(newname)

	if oldClean == newClean {
		return nil
	}
	if oldClean == "/" || newClean == "/" {
		return errno("rename", oldClean, syscall.EACCES)
	}

	info, err := f.Stat(oldClean)
	if err != nil {
		return err
	}

	ctx, cancel := metaContext(context.Background())
	defer cancel()

	if !info.IsDir() {
		newKey := f.key(newClean)

		if exists, err := f.prefixHasObjects(ctx, f.keyDir(newClean)); err != nil {
			return err
		} else if exists {
			return errno("rename", newClean, syscall.EISDIR)
		}

		if err := f.copyKey(ctx, f.key(oldClean), newKey); err != nil {
			return err
		}

		return f.removeKey(ctx, f.key(oldClean))
	}

	oldPrefix := f.keyDir(oldClean)
	newPrefix := f.keyDir(newClean)
	if oldPrefix == "" {
		return errno("rename", oldClean, syscall.EACCES)
	}

	if strings.HasPrefix(newPrefix, oldPrefix) {
		return errno("rename", newClean, syscall.EINVAL)
	}

	if exists, err := f.prefixHasObjects(ctx, newPrefix); err != nil {
		return err
	} else if exists {
		return errno("rename", newClean, syscall.ENOTEMPTY)
	}

	keys, err := f.listAllKeys(ctx, oldPrefix)
	if err != nil {
		return err
	}

	for _, key := range keys {
		target := newPrefix + strings.TrimPrefix(key, oldPrefix)
		if err := f.copyKey(ctx, key, target); err != nil {
			return err
		}
	}

	return f.deleteKeys(ctx, keys)
}

func (f *FS) copyKey(ctx context.Context, fromKey, toKey string) error {
	_, err := f.client.s3.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(f.bucket),
		Key:        aws.String(toKey),
		CopySource: aws.String(copySource(f.bucket, fromKey)),
	})
	if err != nil {
		return err
	}

	f.logger.Debug("copied S3 object", "bucket", f.bucket, "from", fromKey, "to", toKey)

	return nil
}

func (f *FS) listAllKeys(ctx context.Context, prefix string) ([]string, error) {
	var (
		keys  []string
		token *string
	)

	for {
		out, err := f.client.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(f.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}

		for i := range out.Contents {
			keys = append(keys, aws.ToString(out.Contents[i].Key))
		}

		if !aws.ToBool(out.IsTruncated) {
			break
		}

		token = out.NextContinuationToken
	}

	return keys, nil
}

func (f *FS) deleteKeys(ctx context.Context, keys []string) error {
	const batchSize = 1000

	for start := 0; start < len(keys); start += batchSize {
		end := min(start+batchSize, len(keys))

		objects := make([]s3types.ObjectIdentifier, 0, end-start)
		for _, key := range keys[start:end] {
			objects = append(objects, s3types.ObjectIdentifier{Key: aws.String(key)})
		}

		if _, err := f.client.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(f.bucket),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		}); err != nil {
			return err
		}
	}

	return nil
}
