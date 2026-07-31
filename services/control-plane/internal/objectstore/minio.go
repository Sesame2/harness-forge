package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIO struct {
	client *minio.Client
	bucket string
}

func NewMinIO(ctx context.Context, endpoint, accessKey, secretKey, bucket string) (*MinIO, error) {
	secure := false
	host := endpoint
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Path != "" {
			return nil, fmt.Errorf("invalid MinIO endpoint %q", endpoint)
		}
		host, secure = parsed.Host, parsed.Scheme == "https"
	}
	client, err := minio.New(host, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: secure})
	if err != nil {
		return nil, fmt.Errorf("create MinIO client: %w", err)
	}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check MinIO bucket %q: %w", bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("MinIO bucket %q does not exist", bucket)
	}
	return &MinIO{client: client, bucket: bucket}, nil
}

func (s *MinIO) Put(ctx context.Context, key string, reader io.Reader, options PutOptions) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, reader, -1, minio.PutObjectOptions{ContentType: options.ContentType})
	if err != nil {
		return fmt.Errorf("put object %q: %w", key, err)
	}
	return nil
}

func (s *MinIO) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("open object %q: %w", key, err)
	}
	if _, err := object.Stat(); err != nil {
		_ = object.Close()
		return nil, fmt.Errorf("stat object %q: %w", key, err)
	}
	return object, nil
}

func (s *MinIO) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete object %q: %w", key, err)
	}
	return nil
}

func (s *MinIO) DeletePrefix(ctx context.Context, prefix string) error {
	objects := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	for result := range s.client.RemoveObjects(ctx, s.bucket, objects, minio.RemoveObjectsOptions{}) {
		if result.Err != nil {
			return fmt.Errorf("delete object %q: %w", result.ObjectName, result.Err)
		}
	}
	return nil
}

func (s *MinIO) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat object %q: %w", key, err)
	}
	return ObjectInfo{Size: info.Size, ContentType: info.ContentType, LastModified: info.LastModified}, nil
}
