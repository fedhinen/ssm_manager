package theme

import (
	"bytes"
	"testing"
)

func TestNewPlainFallbacks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     []string
		disable bool
	}{
		{name: "flag", env: []string{"TERM=xterm-256color"}, disable: true},
		{name: "NO_COLOR even empty", env: []string{"TERM=xterm-256color", "NO_COLOR="}},
		{name: "dumb terminal", env: []string{"TERM=dumb"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := New(&bytes.Buffer{}, tt.env, tt.disable, true); !got.Plain {
				t.Fatal("New() did not enable plain fallback")
			}
		})
	}
}
