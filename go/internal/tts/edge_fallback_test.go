package tts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// v2 拨号失败时必须自动落到 v1 并更新偏好。
func TestEdgeSynthesizeFallsBackToV1(t *testing.T) {
	f := newFakeEdge(t)
	dialer := &websocketDialer{
		handshakeTO: 5 * time.Second,
		dialEdgeOverride: func(endpoint edgeEndpoint) (*websocket.Conn, error) {
			if endpoint.name == "v2" {
				return nil, errors.New("v2 refused")
			}
			conn, _, err := websocket.DefaultDialer.Dial(f.url(), nil)
			return conn, err
		},
	}
	svc := NewServiceWithDialer(dialer)
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	audio, err := svc.edgeSynthesize(ctx, "fallback test", Options{Voice: "v", Volume: 100})
	if err != nil {
		t.Fatalf("fallback to v1 failed: %v", err)
	}
	if len(audio) == 0 {
		t.Fatal("fallback returned empty audio")
	}
	if svc.preferV2Conv.Load() {
		t.Fatal("preference should flip to v1 after v2 failure")
	}
}
