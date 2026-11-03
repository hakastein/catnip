package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hakastein/catnip/internal/edit"
)

func TestKeep(t *testing.T) {
	tests := []struct {
		name           string
		old, data, out string
	}{
		{"BOM and CRLF of the old content", "\ufeffa\r\n", "b\nc\n", "\ufeffb\r\nc\r\n"},
		{"mixed input over CRLF gets CRLF only", "a\r\nb\r\n", "x\r\ny\nz\r\n", "x\r\ny\r\nz\r\n"},
		{"old content without a line break keeps the input terminators", "x", "a\r\nb\r\n", "a\r\nb"},
		{"empty old content keeps the input as is", "", "a\r\nb\r\n", "a\r\nb\r\n"},
		{"empty old content keeps the input BOM", "", "\ufeffx\n", "\ufeffx\n"},
		{"empty input empties the file", "a\r\n", "", ""},
		{"missing final newline is kept", "same", "same\n", "same"},
		{"final newline is kept", "a\n", "b", "b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.out, string(edit.Keep([]byte(tt.old), []byte(tt.data))))
		})
	}
}
