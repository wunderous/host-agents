package hostagent

import (
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/tcprelay"
)

func TestTCPRelayManagerTracksConcurrentConnections(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var targetWG sync.WaitGroup
	stopTarget := make(chan struct{})
	targetWG.Add(1)
	go func() {
		defer targetWG.Done()
		for {
			conn, acceptErr := target.Accept()
			if acceptErr != nil {
				select {
				case <-stopTarget:
					return
				default:
				}
				continue
			}
			targetWG.Add(1)
			go func() {
				defer targetWG.Done()
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	manager := tcprelay.New()
	session, err := manager.Start(
		"concurrent-test",
		"127.0.0.1",
		0,
		"127.0.0.1",
		target.Addr().(*net.TCPAddr).Port,
	)
	if err != nil {
		close(stopTarget)
		_ = target.Close()
		targetWG.Wait()
		t.Fatal(err)
	}

	const clientCount = 24
	clients := make([]net.Conn, 0, clientCount)
	for i := 0; i < clientCount; i++ {
		client, dialErr := net.Dial("tcp", net.JoinHostPort(session.ListenHost, strconv.Itoa(session.ListenPort)))
		if dialErr != nil {
			manager.Stop(session.SessionID)
			t.Fatal(dialErr)
		}
		clients = append(clients, client)
		if _, writeErr := client.Write([]byte("ping")); writeErr != nil {
			manager.Stop(session.SessionID)
			t.Fatal(writeErr)
		}
		response := make([]byte, 4)
		if _, readErr := io.ReadFull(client, response); readErr != nil {
			manager.Stop(session.SessionID)
			t.Fatal(readErr)
		}
	}

	manager.Stop(session.SessionID)
	for _, client := range clients {
		_ = client.Close()
	}
	close(stopTarget)
	_ = target.Close()
	done := make(chan struct{})
	go func() {
		targetWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("target relay connections did not close")
	}
}
