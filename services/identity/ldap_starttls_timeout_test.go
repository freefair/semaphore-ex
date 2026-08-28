package identity

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func TestDialLDAPBoundsStartTLSNegotiation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		close(accepted)
		time.Sleep(750 * time.Millisecond)
		_ = connection.Close()
	}()

	started := time.Now()
	_, err = dialLDAP(
		"ldap://"+listener.Addr().String(),
		pro_interfaces.LDAPTLSModeStartTLS,
		&tls.Config{MinVersion: tls.VersionTLS12, ServerName: "localhost"},
		100*time.Millisecond,
	)
	<-accepted
	if err == nil {
		t.Fatal("stalled StartTLS negotiation unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("StartTLS negotiation took %s, want bounded failure", elapsed)
	}
}
