package infisicalpam_test

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrokerCloseEndsEverySessionWhenGatewayHangs(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	broker, err := infisicalpam.StartBroker(infisicalpam.BrokerConfig{
		Client: newTestClient(t, env, pamtest.ClientSecret),
		Routes: []infisicalpam.Route{
			{Host: "web-1", AccountPath: "servers/web-1"},
			{Host: "db-1", AccountPath: "servers/db-1"},
		},
		Duration: time.Hour, SocketPath: privateSocketPath(t), CleanupTimeout: 200 * time.Millisecond,
	})
	require.NoError(t, err)
	_, err = proxyExec(t, broker.SocketPath(), "web-1", "true")
	require.NoError(t, err)
	_, err = proxyExec(t, broker.SocketPath(), "db-1", "true")
	require.NoError(t, err)
	env.SetHang(true)

	started := time.Now()
	broker.Close()

	assert.ElementsMatch(t, []string{"session-1", "session-2"}, env.Ended())
	assert.Less(t, time.Since(started), 3*time.Second)
}

func TestConnectStopsWaitingForBrokerReplyOnCancel(t *testing.T) {
	socket := privateSocketPath(t)
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()
	err = infisicalpam.Connect(ctx, socket, "web-1", strings.NewReader(""), io.Discard)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(started), 3*time.Second)
}

func TestAccessEndsSessionItCannotUse(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{AccountType: "postgres"})
	client := newTestClient(t, env, pamtest.ClientSecret)
	token, err := client.Login(context.Background())
	require.NoError(t, err)

	_, err = client.Access(context.Background(), token, infisicalpam.AccessRequest{Path: "servers/db", Duration: time.Hour})

	assert.Equal(t, infisicalpam.ErrorValidation, infisicalpam.CategoryOf(err))
	assert.Equal(t, []string{"session-1"}, env.Ended())
}

func TestBrokerReusesShortSessions(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	broker, err := infisicalpam.StartBroker(infisicalpam.BrokerConfig{
		Client:   newTestClient(t, env, pamtest.ClientSecret),
		Routes:   []infisicalpam.Route{{Host: "web-1", AccountPath: "servers/web-1"}},
		Duration: time.Minute, SocketPath: privateSocketPath(t),
	})
	require.NoError(t, err)
	t.Cleanup(broker.Close)

	for range 3 {
		_, err = proxyExec(t, broker.SocketPath(), "web-1", "true")
		require.NoError(t, err)
	}

	assert.Len(t, env.Accesses(), 1)
}
