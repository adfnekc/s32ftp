package s3fs

import (
	"net/url"
	"path"
	"strings"
)

// CleanFTPPath normalizes an FTP path into a rooted, cleaned form such as "/"
// or "/a/b.txt". Resolution of ".." cannot escape the root.
func CleanFTPPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}

	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	clean := path.Clean(p)
	if clean == "." {
		return "/"
	}

	return clean
}

// joinRoot prepends the configured root prefix to a rooted FTP path and
// returns the corresponding S3 object key. The root path maps to the prefix
// itself (which may be empty, meaning the bucket root).
func joinRoot(rootPrefix, ftpPath string) string {
	clean := CleanFTPPath(ftpPath)
	if clean == "/" {
		return rootPrefix
	}

	return rootPrefix + strings.TrimPrefix(clean, "/")
}

// dirKey returns the object key used as the directory marker for a path. It is
// always either empty (bucket root) or terminated by "/".
func dirKey(rootPrefix, ftpPath string) string {
	k := joinRoot(rootPrefix, ftpPath)
	if k == "" || strings.HasSuffix(k, "/") {
		return k
	}

	return k + "/"
}

// relativeName returns the entry name relative to a parent key prefix.
func relativeName(prefix, key string) string {
	if key == prefix {
		return ""
	}

	return strings.TrimPrefix(strings.TrimSuffix(key, "/"), prefix)
}

// copySource builds the S3 CopySource header value, URL-encoding the key path
// while keeping the separators intact.
func copySource(bucket, key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}

	return url.PathEscape(bucket) + "/" + strings.Join(segments, "/")
}
