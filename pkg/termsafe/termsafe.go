// Package termsafe removes terminal control sequences from text that comes
// from a server, such as task output written by a playbook or a remote host,
// before it reaches a terminal. Such text must not move the cursor, rewrite
// the window title, set the clipboard or hide later output.
package termsafe

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// controlRE matches escape sequences (CSI, string sequences such as OSC and
// DCS ended by BEL, ESC \ or the C1 string terminator, two-character
// sequences, a lone ESC) and C0 and C1 control characters except tab and line
// feed.
var controlRE = regexp.MustCompile(
	"\x1b\\[[0-?]*[ -/]*[@-~]" +
		"|\x1b[\\]PX^_][^\x07\x1b\u009c]*(?:\x07|\x1b\\\\|\u009c)?" +
		"|\x1b[ -/]*[0-~]" +
		"|\x1b" +
		"|[\x00-\x08\x0b-\x1f\x7f\u0080-\u009f]")

// sgrRE matches a Select Graphic Rendition sequence.
var sgrRE = regexp.MustCompile("^\x1b\\[([0-9;]*)m$")

// Reset ends every colour and style. Callers that keep colours append it
// after a line, so a line cannot change how later output looks.
const Reset = "\x1b[0m"

// String removes terminal control sequences from s and drops invalid UTF-8.
// With keepColor, colour sequences that only set colours, bold, dim, italic or
// underline are kept; blink, reverse video and concealed text are not.
func String(s string, keepColor bool) string {
	s = strings.ToValidUTF8(s, "")
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	return controlRE.ReplaceAllStringFunc(s, func(seq string) string {
		if keepColor && isHarmlessSGR(seq) {
			return seq
		}
		return ""
	})
}

func isControl(r rune) bool {
	return (r < 0x20 && r != '\t' && r != '\n') || (r >= 0x7f && r <= 0x9f)
}

// isHarmlessSGR accepts colours (basic, bright, 256 and true colour), their
// defaults, bold, dim, italic, underline and their resets.
func isHarmlessSGR(seq string) bool {
	m := sgrRE.FindStringSubmatch(seq)
	if m == nil {
		return false
	}
	if m[1] == "" {
		return true
	}
	params := strings.Split(m[1], ";")
	for i := 0; i < len(params); i++ {
		n, err := strconv.Atoi(params[i])
		if err != nil {
			return false
		}
		switch {
		case n == 38 || n == 48:
			// Extended colour: 5;index or 2;r;g;b.
			if i+1 < len(params) && params[i+1] == "5" {
				i += 2
			} else if i+1 < len(params) && params[i+1] == "2" {
				i += 4
			} else {
				return false
			}
			if i >= len(params) {
				return false
			}
		case n <= 4, n == 22, n == 23, n == 24, n == 39, n == 49,
			n >= 30 && n <= 37, n >= 40 && n <= 47, n >= 90 && n <= 97, n >= 100 && n <= 107:
		default:
			return false
		}
	}
	return true
}

// maxPending bounds an unfinished escape sequence kept between writes.
const maxPending = 4096

// Writer filters everything written to it with String before passing it on.
// A sequence or UTF-8 character split across two writes is held back until it
// is complete, so it cannot slip through in parts.
type Writer struct {
	w         io.Writer
	keepColor bool
	pending   []byte
}

// NewWriter returns a Writer that filters what it passes to w.
func NewWriter(w io.Writer, keepColor bool) *Writer {
	return &Writer{w: w, keepColor: keepColor}
}

// Write filters p and reports it as written in full; held-back bytes are
// written with a later call.
func (t *Writer) Write(p []byte) (int, error) {
	data := append(t.pending, p...)
	cut := incompleteTail(data)
	t.pending = append([]byte(nil), data[cut:]...)
	if len(t.pending) > maxPending {
		t.pending = nil
	}
	if cut > 0 {
		if _, err := io.WriteString(t.w, String(string(data[:cut]), t.keepColor)); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// incompleteTail returns where an unfinished escape sequence or UTF-8
// character at the end of data starts, or len(data).
func incompleteTail(data []byte) int {
	if i := bytes.LastIndexByte(data, 0x1b); i >= 0 && !sequenceComplete(string(data[i:])) {
		return i
	}
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				return i
			}
			break
		}
	}
	return len(data)
}

// sequenceComplete reports whether the escape sequence at the start of seq
// ends within seq.
func sequenceComplete(seq string) bool {
	if len(seq) < 2 {
		return false
	}
	switch seq[1] {
	case '[':
		return strings.IndexFunc(seq[2:], func(r rune) bool { return r >= '@' && r <= '~' }) >= 0
	case ']', 'P', 'X', '^', '_':
		return strings.ContainsAny(seq[2:], "\x07\u009c") || strings.Contains(seq[2:], "\x1b\\")
	default:
		return strings.IndexFunc(seq[1:], func(r rune) bool { return r >= '0' && r <= '~' }) >= 0
	}
}
