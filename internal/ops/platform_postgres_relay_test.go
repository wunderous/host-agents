package ops

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/exec"
)

func TestPostgreSQLServiceRelayAuthenticatesAndRedactsToken(t *testing.T) {
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

	manager := newPostgreSQLServiceRelayManager()
	token := strings.Repeat("r", 32)
	descriptor, err := manager.start(PostgreSQLServiceRelayArgs{
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

func TestPersistentPostgreSQLServiceRelayRestoresAfterAgentRestart(t *testing.T) {
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

	configDir := t.TempDir()
	token := strings.Repeat("p", 32)
	args := PostgreSQLServiceRelayArgs{
		SessionID:  "persistent-platform-db",
		ListenHost: "127.0.0.1",
		TargetHost: "127.0.0.1",
		TargetPort: target.Addr().(*net.TCPAddr).Port,
		RelayToken: token,
		Persistent: true,
	}
	firstManager := newPersistentPostgreSQLServiceRelayManagerAt(configDir)
	first, err := firstManager.start(args, args.TargetHost, args.TargetPort)
	if err != nil {
		t.Fatal(err)
	}
	firstPort := first["listenPort"].(int)
	if first["persistent"] != true {
		t.Fatalf("expected persistent descriptor: %#v", first)
	}
	if _, ok := first["expiresAt"]; ok {
		t.Fatalf("persistent descriptor must not expose an expiry: %#v", first)
	}
	entries, err := os.ReadDir(configDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one persisted relay state, entries=%v err=%v", entries, err)
	}
	state, err := os.ReadFile(filepath.Join(configDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), token) {
		t.Fatal("persistent relay state contains the raw relay token")
	}

	// Simulate a host-agent process restart without invoking release: the
	// desired relay state remains while the in-memory listener disappears.
	firstManager.mu.Lock()
	oldSession := firstManager.sessions[args.SessionID]
	firstManager.mu.Unlock()
	oldSession.close()

	secondManager := newPersistentPostgreSQLServiceRelayManagerAt(configDir)
	defer secondManager.stop(args.SessionID)
	second, err := secondManager.start(args, args.TargetHost, args.TargetPort)
	if err != nil {
		t.Fatal(err)
	}
	if second["listenPort"] != firstPort || second["persistent"] != true {
		t.Fatalf("restart did not restore the stable relay: first=%#v second=%#v", first, second)
	}

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(firstPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := "restart-safe"
	if _, err := conn.Write([]byte(token + "\n" + payload)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != payload {
		t.Fatalf("restored relay returned %q, want %q", response, payload)
	}

	if !secondManager.stop(args.SessionID) {
		t.Fatal("expected persistent relay release to remove the active listener")
	}
	entries, err = os.ReadDir(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("relay release left persisted state: %v", entries)
	}
}

func TestPostgreSQLServiceRelayConflictDoesNotRevokeOwner(t *testing.T) {
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
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	manager := newPostgreSQLServiceRelayManager()
	ownerToken := strings.Repeat("o", 32)
	conflictingToken := strings.Repeat("c", 32)
	args := PostgreSQLServiceRelayArgs{
		SessionID:  "owned-relay",
		ListenHost: "127.0.0.1",
		TargetHost: "127.0.0.1",
		TargetPort: target.Addr().(*net.TCPAddr).Port,
		RelayToken: ownerToken,
		Persistent: true,
	}
	descriptor, err := manager.start(args, args.TargetHost, args.TargetPort)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.stop(args.SessionID)

	conflict := args
	conflict.RelayToken = conflictingToken
	if _, err := manager.start(conflict, conflict.TargetHost, conflict.TargetPort); err == nil || !strings.Contains(err.Error(), "relay_session_conflict") {
		t.Fatalf("conflicting owner must fail without replacing the active relay: %v", err)
	}

	if _, err := manager.stopOwned(args.SessionID, conflictingToken); err == nil || !strings.Contains(err.Error(), "relay_ownership_mismatch") {
		t.Fatalf("wrong relay capability must not revoke the owner: %v", err)
	}

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(descriptor["listenPort"].(int))))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := "owner-still-connected"
	if _, err := conn.Write([]byte(ownerToken + "\n" + payload)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != payload {
		t.Fatalf("incumbent relay returned %q, want %q", response, payload)
	}

	activeHandoff := args
	activeHandoff.RelayToken = conflictingToken
	activeHandoff.ReplaceExisting = true
	if _, err := manager.start(activeHandoff, activeHandoff.TargetHost, activeHandoff.TargetPort); err == nil || !strings.Contains(err.Error(), "relay_session_conflict") {
		t.Fatalf("persistent recovery must refuse to replace an active relay: %v", err)
	}

	removed, err := manager.stopOwned(args.SessionID, ownerToken)
	if err != nil || !removed {
		t.Fatalf("owner capability should release the relay: removed=%v err=%v", removed, err)
	}
}

func TestPostgreSQLServiceRelayAllowsIdlePersistentRecoveryHandoff(t *testing.T) {
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
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	manager := newPostgreSQLServiceRelayManager()
	oldToken := strings.Repeat("a", 32)
	newToken := strings.Repeat("b", 32)
	args := PostgreSQLServiceRelayArgs{
		SessionID:  "recoverable-relay",
		ListenHost: "127.0.0.1",
		TargetHost: "127.0.0.1",
		TargetPort: target.Addr().(*net.TCPAddr).Port,
		RelayToken: oldToken,
		Persistent: true,
	}
	oldDescriptor, err := manager.start(args, args.TargetHost, args.TargetPort)
	if err != nil {
		t.Fatal(err)
	}
	oldPort := oldDescriptor["listenPort"].(int)

	handoff := args
	handoff.RelayToken = newToken
	handoff.ReplaceExisting = true
	newDescriptor, err := manager.start(handoff, handoff.TargetHost, handoff.TargetPort)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.stop(handoff.SessionID)
	newPort := newDescriptor["listenPort"].(int)
	if newPort == oldPort {
		t.Fatalf("idle recovery should bind a fresh listener after closing the incumbent: old=%d new=%d", oldPort, newPort)
	}

	oldConn, oldDialErr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(oldPort)), time.Second)
	if oldDialErr == nil {
		_ = oldConn.Close()
		t.Fatalf("idle recovery left the incumbent listener open on port %d", oldPort)
	}

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(newPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := "handoff-owner"
	if _, err := conn.Write([]byte(newToken + "\n" + payload)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != payload {
		t.Fatalf("recovered relay returned %q, want %q", response, payload)
	}
}

func TestPostgreSQLServiceRelayExpires(t *testing.T) {
	manager := newPostgreSQLServiceRelayManager()
	descriptor, err := manager.start(PostgreSQLServiceRelayArgs{
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

func TestPostgreSQLServiceRelayExpiryClosesActiveConnections(t *testing.T) {
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

	manager := newPostgreSQLServiceRelayManager()
	token := strings.Repeat("a", 32)
	descriptor, err := manager.start(PostgreSQLServiceRelayArgs{
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

func TestEnsurePostgreSQLServiceRelayRestrictsTargets(t *testing.T) {
	service := &HostOperationsService{postgresqlServiceRelay: newPostgreSQLServiceRelayManager()}
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system", Databases: []string{"testdb"}, ConsumerSecretName: "test-db", ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service", ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"}, RelayDeviceName: "test-postgres-rw"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 32)
	for _, args := range []PostgreSQLServiceRelayArgs{
		{SessionID: "relay-a", ListenHost: "127.0.0.1", TargetHost: "10.0.100.5", RelayToken: token},
		{SessionID: "relay-b", ListenHost: "127.0.0.1", TargetHost: spec.ClusterName + "-rw." + spec.Namespace + ".svc", TargetPort: 5433, RelayToken: token},
		{SessionID: "relay-c", ListenHost: "127.0.0.1", TargetHost: "opute.example.com", RelayToken: token},
	} {
		if _, err := service.ensurePostgreSQLServiceRelay(context.Background(), spec, args); err == nil {
			t.Fatalf("expected relay target restriction for %#v", args)
		}
	}
	// The CNPG read/write Service (port 5432) and loopback port-forwards on
	// any port are the only permitted targets; all must start and revoke.
	for _, args := range []PostgreSQLServiceRelayArgs{
		{SessionID: "relay-svc", ListenHost: "127.0.0.1", TargetHost: spec.ClusterName + "-rw." + spec.Namespace + ".svc", RelayToken: token},
		{SessionID: "relay-loop", ListenHost: "127.0.0.1", TargetHost: "127.0.0.1", RelayToken: token},
		{SessionID: "relay-loop-port", ListenHost: "127.0.0.1", TargetHost: "127.0.0.1", TargetPort: 15432, RelayToken: token},
	} {
		if _, err := service.ensurePostgreSQLServiceRelay(context.Background(), spec, args); err != nil {
			t.Fatalf("expected relay target %s:%d to be accepted: %v", args.TargetHost, args.TargetPort, err)
		}
		service.revokeAllPostgreSQLServiceRelays()
	}
}

func TestEnsurePostgreSQLServiceRelayCreatesHostForwardWhenTargetOmitted(t *testing.T) {
	service := &HostOperationsService{postgresqlServiceRelay: newPostgreSQLServiceRelayManager()}
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
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system", Databases: []string{"testdb"}, ConsumerSecretName: "test-db", ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service", ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"}, RelayDeviceName: "test-postgres-rw"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ensurePostgreSQLServiceRelay(context.Background(), spec, PostgreSQLServiceRelayArgs{
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
	if port, ok := first["targetPort"].(int); !ok || port <= 0 || port == postgresqlServicePort {
		t.Fatalf("expected a positive non-default forward port in the descriptor: %#v", first)
	}
	service.revokeAllPostgreSQLServiceRelays()

	// A second relay for the same Service must reuse the existing forward
	// device instead of recreating it, so concurrent consumers stay stable.
	second, err := service.ensurePostgreSQLServiceRelay(context.Background(), spec, PostgreSQLServiceRelayArgs{
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
	service.revokeAllPostgreSQLServiceRelays()
}
