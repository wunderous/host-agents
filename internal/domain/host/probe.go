package host

import (
	"context"
	"fmt"
	"net"
	"time"
)

// probeTCPPort and envOr live here because the host domain is the only place
// that reaches the machine's own environment and listening ports. Both were in
// the flat service.go; neither was ever shared.

func probeTCPPort(ctx context.Context, host string, port int) (bool, error) {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}
