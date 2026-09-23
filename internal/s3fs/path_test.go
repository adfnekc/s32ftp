package s3fs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanFTPPath(t *testing.T) {
	cases := map[string]string{
		"":            "/",
		"/":           "/",
		".":           "/",
		"a":           "/a",
		"/a/b":        "/a/b",
		"/a/../b":     "/b",
		"/../../ etc": "/ etc",
		"/a/b/":       "/a/b",
	}

	for in, want := range cases {
		require.Equal(t, want, CleanFTPPath(in), "input %q", in)
	}
}

func TestJoinRoot(t *testing.T) {
	require.Equal(t, "", joinRoot("", "/"))
	require.Equal(t, "file.txt", joinRoot("", "/file.txt"))
	require.Equal(t, "a/b/file.txt", joinRoot("", "/a/b/file.txt"))
	require.Equal(t, "root/", joinRoot("root/", "/"))
	require.Equal(t, "root/file.txt", joinRoot("root/", "/file.txt"))
	require.Equal(t, "root/a/b", joinRoot("root/", "/a/b"))
}

func TestDirKey(t *testing.T) {
	require.Equal(t, "", dirKey("", "/"))
	require.Equal(t, "a/", dirKey("", "/a"))
	require.Equal(t, "a/b/", dirKey("", "/a/b"))
	require.Equal(t, "root/", dirKey("root/", "/"))
	require.Equal(t, "root/a/", dirKey("root/", "/a"))
}

func TestRelativeName(t *testing.T) {
	require.Equal(t, "b", relativeName("a/", "a/b"))
	require.Equal(t, "b", relativeName("a/", "a/b/"))
	require.Equal(t, "b/c", relativeName("a/", "a/b/c"))
	require.Equal(t, "", relativeName("a/", "a/"))
}

func TestCopySource(t *testing.T) {
	require.Equal(t, "bucket/a/b.txt", copySource("bucket", "a/b.txt"))
	require.Equal(t, "bucket/a%20b/c%23d.txt", copySource("bucket", "a b/c#d.txt"))
}
