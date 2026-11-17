package bthome

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebsocketRequestsSerializeByBridge(t *testing.T) {
	connected := make(chan struct{}, 2)
	release := make(chan struct{})
	released := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		connected <- struct{}{}
		<-release
		if err := conn.WriteMessage(websocket.TextMessage, []byte("aabbccddeeffd2fc4000010164")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	// Release blocked handlers even when an assertion fails.
	defer server.Close()
	defer func() {
		if !released {
			close(release)
		}
	}()

	host := strings.TrimPrefix(server.URL, "http://")
	first := &bthome{Name: "first", Host: host, MacAddress: "aabbccddeeff", Timeout: 2000}
	second := &bthome{Name: "second", Host: host, MacAddress: "aabbccddeeff", Timeout: 2000}
	batch := &base{Devices: []*bthome{second}}
	results := make(chan error, 2)
	go func() {
		_, err := first.websocketConnectWithResponse()
		results <- err
	}()
	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("first command did not connect")
	}

	started := make(chan struct{})
	go func() {
		close(started)
		_, err := batch.websocketConnectWithResponses([]*bthome{second})
		results <- err
	}()
	<-started
	select {
	case <-connected:
		t.Fatal("second command connected while the first was still waiting for its response")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	released = true
	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("second command did not connect after the first completed")
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("command failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("command did not complete")
		}
	}
}
