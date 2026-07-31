package projects

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalid         = errors.New("invalid request")
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("resource conflict")
	ErrPayloadTooLarge = errors.New("payload too large")
)

type Project struct {
	ID                      uuid.UUID  `json:"id"`
	Name                    string     `json:"name"`
	ProfileID               string     `json:"profile_id"`
	ProfileVersion          string     `json:"profile_version"`
	AcceptedInputMediaTypes []string   `json:"accepted_input_media_types"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	DeletedAt               *time.Time `json:"-"`
}

type InputFile struct {
	ID           uuid.UUID `json:"id"`
	ProjectID    uuid.UUID `json:"project_id"`
	DisplayName  string    `json:"display_name"`
	MediaType    string    `json:"media_type"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256Digest string    `json:"sha256_digest"`
	ObjectKey    string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}
