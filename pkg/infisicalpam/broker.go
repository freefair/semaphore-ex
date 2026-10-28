package infisicalpam

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	requestPrefix       = "CONNECT "
	maximumRequestBytes = 512
	requestReadTimeout  = 10 * time.Second
	closeTimeout        = 5 * time.Second
	// connectReplyTimeout bounds the wait for the broker's reply, which
	// includes a login and a session request.
	connectReplyTimeout = 90 * time.Second
	maximumSocketPath   = 103
	// sessionRenewMargin opens a fresh session before the current one would
	// expire in the middle of a new connection.
	sessionRenewMargin = time.Minute
)

// RouteHostPattern is the set of SSH host names a route can match. The name is
// written into a generated ssh_config, so anything outside it is config syntax.
var RouteHostPattern = regexp.MustCompile(
	`^[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?(\.[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?)*$`)

var replyCategoryPattern = regexp.MustCompile(`^[a-z_]{1,32}$`)

// Route sends SSH connections for one host through one PAM account.
type Route struct {
	Host        string
	AccountPath string
}

// ValidateRoutes checks hosts and account paths and rejects duplicate hosts.
func ValidateRoutes(routes []Route) error {
	seen := map[string]bool{}
	for _, route := range routes {
		host := strings.ToLower(route.Host)
		if len(host) > 253 || !RouteHostPattern.MatchString(host) {
			return newError(ErrorValidation, "configure", fmt.Errorf("route host %q is invalid", route.Host))
		}
		if seen[host] {
			return newError(ErrorValidation, "configure", fmt.Errorf("route host %q is mapped twice", route.Host))
		}
		seen[host] = true
		if err := ValidateAccountPath(route.AccountPath); err != nil {
			return err
		}
	}
	return nil
}

// BrokerConfig configures one task's broker.
type BrokerConfig struct {
	Client     *Client
	Routes     []Route
	Duration   time.Duration
	Reason     string
	SocketPath string
	// Log receives value-free event lines for the task log.
	Log func(string)
	// CleanupTimeout bounds each step of ending a session; zero means 5s.
	CleanupTimeout time.Duration
}

// Broker serves PAM-backed SSH streams on a Unix socket for one task. It
// creates a session per account on first use and ends every session on Close.
// The socket is protected by file permissions in the same way as the task
// SSH agent socket.
type Broker struct {
	config   BrokerConfig
	routes   map[string]string
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	mu       sync.Mutex
	token    AccessToken
	sessions map[string]*Session
	retired  []*Session
	closed   bool
}

func StartBroker(config BrokerConfig) (*Broker, error) {
	if config.Client == nil {
		return nil, newError(ErrorValidation, "configure", errors.New("client is required"))
	}
	if len(config.Routes) == 0 {
		return nil, newError(ErrorValidation, "configure", errors.New("at least one route is required"))
	}
	if err := ValidateRoutes(config.Routes); err != nil {
		return nil, err
	}
	if err := (AccessRequest{Path: config.Routes[0].AccountPath, Duration: config.Duration, Reason: config.Reason}).validate(); err != nil {
		return nil, err
	}
	routes := make(map[string]string, len(config.Routes))
	for _, route := range config.Routes {
		routes[strings.ToLower(route.Host)] = route.AccountPath
	}
	listener, err := listenUnix(config.SocketPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	broker := &Broker{
		config: config, routes: routes, listener: listener,
		ctx: ctx, cancel: cancel, sessions: map[string]*Session{},
	}
	broker.wg.Add(1)
	go broker.serve()
	return broker, nil
}

func listenUnix(path string) (net.Listener, error) {
	if path == "" {
		return nil, newError(ErrorValidation, "configure", errors.New("socket path is required"))
	}
	// sockaddr_un holds 104 bytes on macOS and 108 on Linux.
	if len(path) > maximumSocketPath {
		return nil, newError(ErrorValidation, "listen", fmt.Errorf("socket path exceeds %d bytes; shorten the temporary directory", maximumSocketPath))
	}
	// The parent directory must already be private: the socket exists with
	// default permissions until the chmod below, and changing the umask would
	// race with every other goroutine in the process.
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, newError(ErrorValidation, "listen", errors.New("socket directory must be private (0700)"))
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, newError(ErrorTransport, "listen", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, newError(ErrorTransport, "listen", err)
	}
	return listener, nil
}

// renewMargin is at most a quarter of the session duration, so short sessions
// are still reused.
func renewMargin(duration time.Duration) time.Duration {
	if quarter := duration / 4; quarter < sessionRenewMargin {
		return quarter
	}
	return sessionRenewMargin
}

// Hosts lists the routed host names in lower case.
func (b *Broker) Hosts() []string {
	hosts := make([]string, 0, len(b.routes))
	for _, route := range b.config.Routes {
		hosts = append(hosts, strings.ToLower(route.Host))
	}
	return hosts
}

func (b *Broker) SocketPath() string { return b.config.SocketPath }

func (b *Broker) serve() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			b.handle(conn)
		}()
	}
}

func (b *Broker) handle(client net.Conn) {
	defer client.Close()
	_ = client.SetReadDeadline(time.Now().Add(requestReadTimeout))
	reader := bufio.NewReaderSize(client, maximumRequestBytes)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Time{})
	host := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(line), requestPrefix)))
	if !strings.HasPrefix(string(line), requestPrefix) || !RouteHostPattern.MatchString(host) {
		_, _ = io.WriteString(client, "ERR validation\n")
		return
	}
	path, ok := b.routes[host]
	if !ok {
		_, _ = io.WriteString(client, "ERR not_routed\n")
		return
	}
	target, err := b.dial(path)
	if err != nil {
		category := CategoryOf(err)
		if category == "" {
			category = ErrorTransport
		}
		b.log(fmt.Sprintf("Infisical PAM connection to %s via %s failed: %s", host, path, category))
		_, _ = io.WriteString(client, "ERR "+string(category)+"\n")
		return
	}
	defer target.Close()
	if _, err := io.WriteString(client, "OK\n"); err != nil {
		return
	}
	splice(b.ctx, client, reader, target)
}

func (b *Broker) dial(path string) (net.Conn, error) {
	session, err := b.session(path)
	if err != nil {
		return nil, err
	}
	return session.Dial(b.ctx)
}

func (b *Broker) session(path string) (*Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, newError(ErrorUnavailable, "access", errors.New("broker closed"))
	}
	now := time.Now()
	if session, ok := b.sessions[path]; ok {
		if !session.Expired(now.Add(renewMargin(b.config.Duration))) {
			return session, nil
		}
		b.retired = append(b.retired, session)
		delete(b.sessions, path)
	}
	if b.token.Value == "" || (!b.token.ExpiresAt.IsZero() && !now.Add(sessionRenewMargin).Before(b.token.ExpiresAt)) {
		token, err := b.config.Client.Login(b.ctx)
		if err != nil {
			return nil, err
		}
		b.token = token
	}
	session, err := b.config.Client.Access(b.ctx, b.token, AccessRequest{
		Path: path, Duration: b.config.Duration, Reason: b.config.Reason,
	})
	if err != nil {
		return nil, err
	}
	b.sessions[path] = session
	b.log(fmt.Sprintf("Infisical PAM session opened for %s", path))
	return session, nil
}

// Close stops accepting connections, waits for open streams and ends every
// session it opened. It is safe to call more than once.
func (b *Broker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	sessions := append([]*Session(nil), b.retired...)
	for _, session := range b.sessions {
		sessions = append(sessions, session)
	}
	token := b.token
	b.token = AccessToken{}
	b.mu.Unlock()

	_ = b.listener.Close()
	b.cancel()
	b.wg.Wait()
	_ = os.Remove(b.config.SocketPath)

	for _, session := range sessions {
		if session.Expired(time.Now()) {
			continue
		}
		cancelErr := withTimeout(b.cleanupTimeout(), session.Cancel)
		endErr := withTimeout(b.cleanupTimeout(), func(ctx context.Context) error {
			return b.config.Client.EndSession(ctx, token, session.ID)
		})
		if cancelErr != nil && endErr != nil {
			b.log(fmt.Sprintf("Infisical PAM session %s could not be ended: %s", session.ID, CategoryOf(endErr)))
		}
	}
}

func (b *Broker) cleanupTimeout() time.Duration {
	if b.config.CleanupTimeout > 0 {
		return b.config.CleanupTimeout
	}
	return closeTimeout
}

// withTimeout gives each cleanup step its own budget, so one hanging gateway
// cannot use up the time the API fallback and later sessions need.
func withTimeout(timeout time.Duration, step func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return step(ctx)
}

func (b *Broker) log(message string) {
	if b.config.Log != nil {
		b.config.Log(message)
	}
}

// splice copies both directions until either side ends or ctx is cancelled.
func splice(ctx context.Context, client net.Conn, clientReader io.Reader, target net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(target, clientReader)
		closeWrite(target)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, target)
		closeWrite(client)
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	_ = client.Close()
	_ = target.Close()
	<-done
}

func closeWrite(conn net.Conn) {
	if writer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = writer.CloseWrite()
	}
}

// Connect is the ProxyCommand side: it asks the broker at socketPath for host
// and splices the stream to stdin and stdout.
func Connect(ctx context.Context, socketPath, host string, stdin io.Reader, stdout io.Writer) error {
	host = strings.ToLower(host)
	if !RouteHostPattern.MatchString(host) {
		return newError(ErrorValidation, "connect", errors.New("host is invalid"))
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return newError(ErrorTransport, "connect", err)
	}
	defer conn.Close()
	// Cancellation closes the socket, which unblocks every read and write.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(connectReplyTimeout))
	if _, err := io.WriteString(conn, requestPrefix+host+"\n"); err != nil {
		return newError(ErrorTransport, "connect", err)
	}
	reader := bufio.NewReaderSize(conn, maximumRequestBytes)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return newError(ErrorTransport, "connect", err)
	}
	_ = conn.SetDeadline(time.Time{})
	reply := strings.TrimSpace(string(line))
	if reply != "OK" {
		category := ErrorCategory(strings.TrimPrefix(reply, "ERR "))
		if !replyCategoryPattern.MatchString(string(category)) {
			category = ErrorResponseInvalid
		}
		return &Error{Category: category, Operation: "connect"}
	}
	// The stream ends when the target side ends. stdin reaching EOF only
	// half-closes the request direction, as a TCP connection would.
	output := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(conn, stdin)
		if copyErr == nil {
			closeWrite(conn)
		}
	}()
	go func() {
		_, copyErr := io.Copy(stdout, reader)
		output <- copyErr
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-output:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return newError(ErrorTransport, "connect", err)
		}
		return nil
	}
}
