package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type memoryObject struct {
	data []byte
	info ObjectInfo
}

type Memory struct {
	mu      sync.RWMutex
	objects map[string]memoryObject
}

func NewMemory() *Memory { return &Memory{objects: make(map[string]memoryObject)} }

func (s *Memory) Put(_ context.Context, key string, reader io.Reader, options PutOptions) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = memoryObject{data: data, info: ObjectInfo{Size: int64(len(data)), ContentType: options.ContentType, LastModified: time.Now().UTC()}}
	return nil
}
func (s *Memory) Open(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("object %q not found", key)
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), object.data...))), nil
}
func (s *Memory) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}
func (s *Memory) DeletePrefix(_ context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			delete(s.objects, key)
		}
	}
	return nil
}
func (s *Memory) Stat(_ context.Context, key string) (ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[key]
	if !ok {
		return ObjectInfo{}, fmt.Errorf("object %q not found", key)
	}
	return object.info, nil
}
