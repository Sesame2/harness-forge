package conversations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMessageTitleNormalizesAndTruncatesUnicode(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{" \n hello\t  world \n", "hello world"},
		{strings.Repeat("界", 41), strings.Repeat("界", 40)},
		{"a\u3000b", "a b"},
	} {
		if got := messageTitle(tc.input); got != tc.want {
			t.Fatalf("title = %q, want %q", got, tc.want)
		}
	}
}

func TestConversationAndMessageValidateBeforePersistence(t *testing.T) {
	service := NewService(nil, nil)
	for _, blank := range []string{"", " \n\t"} {
		if _, err := service.RenameConversation(context.Background(), uuid.New(), blank); !errors.Is(err, ErrInvalid) {
			t.Fatalf("rename: %v", err)
		}
		if _, err := service.SubmitMessage(context.Background(), uuid.New(), blank); !errors.Is(err, ErrInvalid) {
			t.Fatalf("message: %v", err)
		}
	}
}
