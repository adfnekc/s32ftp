package s3fs

import (
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func putObjectInput(bucket, key string, body io.Reader, contentType string) *s3.PutObjectInput {
	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   body,
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	return input
}

func headObjectInput(bucket, key string) *s3.HeadObjectInput {
	return &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}
}

func sizeOf(v *int64) int64 { return aws.ToInt64(v) }

func timeOf(v *time.Time) time.Time { return aws.ToTime(v) }

func osTimeNow() time.Time { return time.Now() }
