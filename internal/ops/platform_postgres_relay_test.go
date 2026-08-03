package ops

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/exec"
)

func TestPlatformPostgresRelayAuthenticatesAndRedactsToken(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		conn, acceptErr := target.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	manager := newPlatformPostgresRelayManager()
	token := strings.Repeat("r", 32)
	descriptor, err := manager.start(PlatformPostgresRelayArgs{
		SessionID:  "platform-db-test",
		ListenHost: "127.0.0.1",
		TargetHost: "127.0.0.1",
		TargetPort: target.Addr().(*net.TCPAddr).Port,
		RelayToken: token,
		TTLSeconds: 30,
	}, "127.0.0.1", target.Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.stop("platform-db-test")
	if descriptor["persistent"] != false || descriptor["authMode"] != "token-line" {
		t.Fatalf("unexpected relay descriptor: %#v", descriptor)
	}
	if _, leaked := descriptor["relayToken"]; leaked {
		t.Fatal("relay descriptor leaked the authentication token")
	}

	port := descriptor["listenPort"].(int)
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := strings.Repeat("x", 2048)
	if _, err := conn.Write([]byte(token + "\n" + payload)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != payload {
		t.Fatalf("relay returned %d bytes, want %d", len(buf), len(payload))
	}
	if !manager.stop("platform-db-test") {
		t.Fatal("expected explicit revocation to close the relay")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("revocation left the authenticated connection open")
	}
}

func TestPlatformPostgresRelayExpires(t *testing.T) {
	manager := newPlatformPostgresRelayManager()
	descriptor, err := manager.start(PlatformPostgresRelayArgs{
		SessionID:  "platform-db-expiry",
		ListenHost: "127.0.0.1",
		TargetHost: "127.0.0.1",
		TargetPort: 1,
		RelayToken: strings.Repeat("e", 32),
		TTLSeconds: 10,
	}, "127.0.0.1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(descriptor["expiresAt"].(string), "T") {
		t.Fatalf("invalid expiry descriptor: %#v", descriptor)
	}
	port := descriptor["listenPort"].(int)
	dial := func() error {
		conn, dialErr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
		if dialErr != nil {
			return dialErr
		}
		_ = conn.Close()
		return nil
	}
	// The relay must expire on its own without explicit revocation.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if dial() != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if dial() == nil {
		t.Fatal("relay listener remained open after automatic TTL expiry")
	}
}

func TestPlatformPostgresRelayExpiryClosesActiveConnections(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			conn, acceptErr := target.Accept()
			if acceptErr != nil {
				return
			}
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()

	manager := newPlatformPostgresRelayManager()
	token := strings.Repeat("a", 32)
	descriptor, err := manager.start(PlatformPostgresRelayArgs{
		SessionID:  "platform-db-expiry-active",
		ListenHost: "127.0.0.1",
		TargetHost: "127.0.0.1",
		TargetPort: target.Addr().(*net.TCPAddr).Port,
		RelayToken: token,
		TTLSeconds: 10,
	}, "127.0.0.1", target.Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	port := descriptor["listenPort"].(int)
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(token + "\nping")); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(conn, payload); err != nil || string(payload) != "ping" {
		t.Fatalf("authenticated relay connection did not pass traffic: %v %q", err, payload)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if _, readErr := conn.Read(make([]byte, 1)); readErr != nil {
			return
		}
	}
	t.Fatal("authenticated relay connection stayed open after automatic TTL expiry")
}

func TestEnsurePlatformPostgresRelayRestrictsTargets(t *testing.T) {
	service := &HostOperationsService{platformPostgresRelay: newPlatformPostgresRelayManager()}
	spec, err := validatePlatformPostgresSpec(PlatformPostgresArgs{VMName: "opute-local"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 32)
	for _, args := range []PlatformPostgresRelayArgs{
		{SessionID: "relay-a", ListenHost: "127.0.0.1", TargetHost: "10.0.100.5", RelayToken: token},
		{SessionID: "relay-b", ListenHost: "127.0.0.1", TargetHost: spec.ClusterName + "-rw." + spec.Namespace + ".svc", TargetPort: 5433, RelayToken: token},
		{SessionID: "relay-c", ListenHost: "127.0.0.1", TargetHost: "opute.example.com", RelayToken: token},
	} {
		if _, err := service.ensurePlatformPostgresRelay(context.Background(), spec, args); err == nil {
			t.Fatalf("expected relay target restriction for %#v", args)
		}
	}
	// The CNPG read/write Service (port 5432) and loopback port-forwards on
	// any port are the only permitted targets; all must start and revoke.
	for _, args := range []PlatformPostgresRelayArgs{
		{SessionID: "relay-svc", ListenHost: "127.0.0.1", TargetHost: spec.ClusterName + "-rw." + spec.Namespace + ".svc", RelayToken: token},
		{SessionID: "relay-loop", ListenHost: "127.0.0.1", TargetHost: "127.0.0.1", RelayToken: token},
		{SessionID: "relay-loop-port", ListenHost: "127.0.0.1", TargetHost: "127.0.0.1", TargetPort: 15432, RelayToken: token},
	} {
		if _, err := service.ensurePlatformPostgresRelay(context.Background(), spec, args); err != nil {
			t.Fatalf("expected relay target %s:%d to be accepted: %v", args.TargetHost, args.TargetPort, err)
		}
		service.revokeAllPlatformPostgresRelays()
	}
}

func TestEnsurePlatformPostgresRelayCreatesHostForwardWhenTargetOmitted(t *testing.T) {
	service := &HostOperationsService{platformPostgresRelay: newPlatformPostgresRelayManager()}
	deviceAdds := 0
	existingDevice := ""
	service.commandRunnerFn = func(args []string, onData func(string), timeout time.Duration) (exec.Result, error) {
		if args[0] == "config" && args[1] == "device" {
			switch args[2] {
			case "show":
				if existingDevice != "" {
					return exec.Result{Stdout: existingDevice}, nil
				}
				return exec.Result{ExitCode: 1, Stderr: "not found"}, nil
			case "remove":
				existingDevice = ""
				return exec.Result{}, nil
			case "add":
				deviceAdds++
				listenArg := ""
				for _, arg := range args[3:] {
					if strings.HasPrefix(arg, "listen=tcp:") {
						listenArg = strings.TrimPrefix(arg, "listen=")
						break
					}
				}
				existingDevice = "listen: " + listenArg + "\nconnect: tcp:10.43.141.91:5432\n"
				return exec.Result{}, nil
			}
		}
		return exec.Result{}, errors.New("unexpected command")
	}
	service.kubectlRunner = func(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error) {
		if kubectlArgs[0] == "get" && kubectlArgs[1] == "service" {
			return `{"spec":{"clusterIP":"10.43.141.91"}}`, nil
		}
		return "", errors.New("unexpected kubectl call")
	}
	spec, err := validatePlatformPostgresSpec(PlatformPostgresArgs{VMName: "opute-local"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ensurePlatformPostgresRelay(context.Background(), spec, PlatformPostgresRelayArgs{
		SessionID:  "relay-managed",
		ListenHost: "127.0.0.1",
		RelayToken: strings.Repeat("m", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if deviceAdds != 1 {
		t.Fatalf("expected exactly one proxy device add, got %d", deviceAdds)
	}
	if port, ok := first["targetPort"].(int); !ok || port <= 0 || port == platformPostgresServicePort {
		t.Fatalf("expected a positive non-default forward port in the descriptor: %#v", first)
	}
	service.revokeAllPlatformPostgresRelays()

	// A second relay for the same Service must reuse the existing forward
	// device instead of recreating it, so concurrent consumers stay stable.
	second, err := service.ensurePlatformPostgresRelay(context.Background(), spec, PlatformPostgresRelayArgs{
		SessionID:  "relay-managed-2",
		ListenHost: "127.0.0.1",
		RelayToken: strings.Repeat("n", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if deviceAdds != 1 {
		t.Fatalf("expected the forward device to be reused, adds=%d", deviceAdds)
	}
	if first["targetPort"] != second["targetPort"] {
		t.Fatalf("expected the same forward port to be reused: %v vs %v", first["targetPort"], second["targetPort"])
	}
	service.revokeAllPlatformPostgresRelays()
}
