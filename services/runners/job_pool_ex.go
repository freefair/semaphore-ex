package runners

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"net/url"
	"strings"
)

func runnerTransportTrust(webHost string, conn *util.RunnerConnectionConfig) db.RunnerTransportTrust {
	parsed, err := url.Parse(webHost)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return db.RunnerTransportPlaintext
	}
	if conn == nil {
		return db.RunnerTransportSystemCA
	}
	if conn.SkipTLSVerify {
		return db.RunnerTransportInsecure
	}
	if conn.ServerCACertFile != "" {
		return db.RunnerTransportCustomCA
	}
	return db.RunnerTransportSystemCA
}
