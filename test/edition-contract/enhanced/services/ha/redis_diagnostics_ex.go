package ha

import (
	"context"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// Sentinel clients discover their data-node address dynamically. Report the
// configured backend identity instead of the client's initial/default address.
type sharedRedisDiagnostics struct {
	client pro_interfaces.ClusterRedisDiagnosticsClient
	shared pro_interfaces.HARedis
}

func (d sharedRedisDiagnostics) RedisDiagnostics(ctx context.Context) (pro_interfaces.RedisInfo, error) {
	info, err := d.client.RedisDiagnostics(ctx)
	info.Addr = d.shared.Describe()
	return info, err
}
