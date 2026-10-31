package output

import (
	"regexp"
	"strings"
)

// terminalControlRE matches escape sequences (CSI, OSC, string and
// two-character sequences, a lone ESC), C0 and C1 control characters except
// tab and line feed. Text from the server, such as task output written by a
// playbook or a remote host, must not move the cursor, rewrite the window
// title or set the clipboard of the terminal running the client.
var terminalControlRE = regexp.MustCompile(
	"\x1b\\[[0-?]*[ -/]*[@-~]" + // CSI
		"|\x1b[\\]PX^_][^\x07\x1b]*(?:\x07|\x1b\\\\)?" + // OSC, DCS, SOS, PM, APC
		"|\x1b[ -/]*[0-~]" + // other escape sequences
		"|\x1b" + // lone ESC
		"|[\x00-\x08\x0b-\x1f\x7f\u0080-\u009f]") // control characters

// sgrRE matches a Select Graphic Rendition sequence: colours and text style only.
var sgrRE = regexp.MustCompile("^\x1b\\[[0-9;]*m$")

// Sanitize removes terminal control sequences from text. With keepColor, SGR
// colour and style sequences are kept; everything else is dropped.
func Sanitize(text string, keepColor bool) string {
	if !strings.ContainsFunc(text, isControl) {
		return text
	}
	return terminalControlRE.ReplaceAllStringFunc(text, func(seq string) string {
		if keepColor && sgrRE.MatchString(seq) {
			return seq
		}
		return ""
	})
}

func isControl(r rune) bool {
	return (r < 0x20 && r != '\t' && r != '\n') || (r >= 0x7f && r <= 0x9f)
}
