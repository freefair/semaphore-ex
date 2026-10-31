package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		keepColor bool
		want      string
	}{
		{"plain text", "ok: [web1]\tchanged", true, "ok: [web1]\tchanged"},
		{"colour kept on a colour terminal", "\x1b[0;32mok\x1b[0m", true, "\x1b[0;32mok\x1b[0m"},
		{"colour dropped without colour", "\x1b[0;32mok\x1b[0m", false, "ok"},
		{"clipboard write (OSC 52)", "a\x1b]52;c;ZXZpbA==\x07b", true, "ab"},
		{"window title with ST", "a\x1b]0;owned\x1b\\b", true, "ab"},
		{"cursor movement", "a\x1b[2J\x1b[Hb", true, "ab"},
		{"carriage return and bell", "visible\rhidden\x07", true, "visiblehidden"},
		{"C1 control", "a\u009b31mb", true, "a31mb"},
		{"lone escape", "a\x1b", true, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Sanitize(tt.in, tt.keepColor))
		})
	}
}

func TestCellStringDropsTerminalControls(t *testing.T) {
	assert.Equal(t, "prod", cellString("prod\x1b]0;owned\x07"))
}
