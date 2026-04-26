package federation

import (
	"context"
	"testing"
)

func TestTraceFederationForward_NoOp(t *testing.T) {
	ctx, span := TraceFederationForward(context.Background(), "peer-east-1")
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
	if span == nil {
		t.Fatal("span should not be nil")
	}
	span.End()
}

func TestTraceFederationForward_MultiplePeers(t *testing.T) {
	peers := []string{"peer-east-1", "peer-west-2", "peer-eu-1"}
	for _, peer := range peers {
		t.Run(peer, func(t *testing.T) {
			ctx, span := TraceFederationForward(context.Background(), peer)
			if ctx == nil {
				t.Fatal("context should not be nil")
			}
			span.End()
		})
	}
}
