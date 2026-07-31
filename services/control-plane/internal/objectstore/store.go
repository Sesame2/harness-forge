package objectstore

import (
	"context"
	"io"
	"time"
)

type Store interface {
	Put(context.Context, string, io.Reader, PutOptions) error
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
	DeletePrefix(context.Context, string) error
	Stat(context.Context, string) (ObjectInfo, error)
}

type PutOptions struct {
	ContentType string
}

type ObjectInfo struct {
	Size         int64
	ContentType  string
	LastModified time.Time
}
