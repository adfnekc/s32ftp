package ftpdriver

import (
	"log/slog"
	"strings"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/adfnekc/s32ftp/internal/s3fs"
)

// Client implements ftpserver.ClientDriver plus the optional extensions the
// server uses for streaming transfers and directory handling.
type Client struct {
	*s3fs.FS

	logger *slog.Logger
}

// NewClient wraps an S3 filesystem view for a single FTP session.
func NewClient(fs *s3fs.FS, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}

	return &Client{FS: fs, logger: logger}
}

// GetHandle implements ftpserver.ClientDriverExtentionFileTransfer.
func (c *Client) GetHandle(name string, flags int, offset int64) (ftpserver.FileTransfer, error) {
	handle, err := c.FS.GetHandle(name, flags, offset)
	if err != nil {
		return nil, err
	}

	return handle, nil
}

// AllocateSpace implements ftpserver.ClientDriverExtensionAllocate. S3
// buckets are effectively unbounded, so the reservation always succeeds.
func (c *Client) AllocateSpace(int) error { return nil }

// Site implements ftpserver.ClientDriverExtensionSite.
func (c *Client) Site(param string) *ftpserver.AnswerCommand {
	fields := strings.Fields(param)
	if len(fields) > 0 && strings.EqualFold(fields[0], "CHMOD") {
		return &ftpserver.AnswerCommand{
			Code:    ftpserver.StatusOK,
			Message: "SITE CHMOD is not supported by the S3 backend and was ignored",
		}
	}

	return &ftpserver.AnswerCommand{
		Code:    ftpserver.StatusCommandNotImplemented,
		Message: "SITE " + strings.TrimSpace(param) + " is not implemented",
	}
}

var (
	_ ftpserver.ClientDriver                      = (*Client)(nil)
	_ ftpserver.ClientDriverExtentionFileTransfer = (*Client)(nil)
	_ ftpserver.ClientDriverExtensionFileList     = (*Client)(nil)
	_ ftpserver.ClientDriverExtensionRemoveDir    = (*Client)(nil)
	_ ftpserver.ClientDriverExtensionAllocate     = (*Client)(nil)
	_ ftpserver.ClientDriverExtensionSite         = (*Client)(nil)
)
