package termsafe

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestString(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		keepColor bool
		want      string
	}{
		{"plain text", "ok: [web1]\tchanged", true, "ok: [web1]\tchanged"},
		{"colour kept on a colour terminal", "\x1b[0;32mok\x1b[0m", true, "\x1b[0;32mok\x1b[0m"},
		{"256 and true colour kept", "\x1b[38;5;8ma\x1b[48;2;1;2;3mb", true, "\x1b[38;5;8ma\x1b[48;2;1;2;3mb"},
		{"colour dropped without colour", "\x1b[0;32mok\x1b[0m", false, "ok"},
		{"concealed text dropped", "\x1b[8mhidden", true, "hidden"},
		{"blink and reverse dropped", "\x1b[5;7mx", true, "x"},
		{"truncated extended colour dropped", "\x1b[38;5mx", true, "x"},
		{"clipboard write (OSC 52)", "a\x1b]52;c;ZXZpbA==\x07b", true, "ab"},
		{"window title with ST", "a\x1b]0;owned\x1b\\b", true, "ab"},
		{"window title with C1 ST", "a\x1b]0;owned\u009cb", true, "ab"},
		{"cursor movement", "a\x1b[2J\x1b[Hb", true, "ab"},
		{"carriage return and bell", "visible\rhidden\x07", true, "visiblehidden"},
		{"C1 CSI", "a\u009b31mb", true, "a31mb"},
		{"invalid UTF-8 C1 byte", "a\x9b31mb", true, "a31mb"},
		{"lone escape", "a\x1b", true, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, String(tt.in, tt.keepColor))
		})
	}
}

func TestWriterHoldsBackSequencesSplitAcrossWrites(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"OSC split", []string{"a\x1b]52;c;", "ZXZpbA==\x07b"}, "ab"},
		{"ESC at the end", []string{"a\x1b", "[2Jb"}, "ab"},
		{"colour split", []string{"\x1b[0;3", "2mok"}, "\x1b[0;32mok"},
		{"C1 split inside UTF-8", []string{"a\xc2", "\x9bb"}, "ab"},
		{"multi-byte character split", []string{"gr\xc3", "\xbcn"}, "grün"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			w := NewWriter(&out, true)
			for _, c := range tt.chunks {
				n, err := w.Write([]byte(c))
				require.NoError(t, err)
				assert.Equal(t, len(c), n)
			}
			assert.Equal(t, tt.want, out.String())
		})
	}
}

func TestWriterDropsAnEndlessSequence(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out, false)
	_, err := w.Write(append([]byte("\x1b]0;"), bytes.Repeat([]byte("x"), maxPending+1)...))
	require.NoError(t, err)
	_, err = w.Write([]byte("\x07after"))
	require.NoError(t, err)

	assert.Equal(t, "after", out.String())
}
