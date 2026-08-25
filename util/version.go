package util

import (
	"strings"
)

var (
	Ver                   = "undefined"
	Commit                = "00000000"
	Date                  = ""
	BuildEdition          = "community"
	CoreRevision          = "0000000000000000000000000000000000000000"
	EnhancedRevision      = ""
	EditionImplementation = "community-1"
)

func Version() string {
	return strings.Join([]string{
		Ver,
		Commit,
		Date,
	}, "-")
}
