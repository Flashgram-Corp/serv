package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/bin"
	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestLayer230AdditionalBotUsernameRejected(t *testing.T) {
	r, _, _, owner, _ := newBotRPCTestRouter(t)
	ctx := WithUserID(context.Background(), owner.ID)
	for _, raw := range []bool{false, true} {
		req := &tg.BotsCheckUsernameRequest{Username: "fresh_rpc_bot"}
		if raw {
			req.Flags.Set(0)
		} else {
			req.Additional = true
		}
		if ok, err := r.onBotsCheckUsername(ctx, req); ok || !tgerr.Is(err, "METHOD_INVALID") {
			t.Fatalf("additional=%v: %v, %v", raw, ok, err)
		}
	}
	if ok, err := r.onBotsCheckUsername(ctx, &tg.BotsCheckUsernameRequest{Username: "fresh_rpc_bot"}); !ok || err != nil {
		t.Fatalf("ordinary username: %v, %v", ok, err)
	}
}

// Layer 230 wallet coverage: the identity and balance surface is implemented
// (see wallet_rpc_test.go), while the chain-dependent parts stay explicitly
// unimplemented and keep emitting the compatibility trace instead of pretending
// telesrv owns a TON node.
func TestLayer230WalletChainMethodsRemainTracedUnimplemented(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	r := New(Config{DC: 2, IP: "127.0.0.1", Port: 2398}, Deps{}, zap.New(core), clock.System)
	for name, query := range map[string]bin.Object{
		"nfts":    &tg.WalletGetNftsRequest{},
		"gasless": &tg.WalletGetGaslessInfoRequest{},
	} {
		var wire bin.Buffer
		request := &tg.InvokeWithLayerRequest{Layer: 230, Query: query}
		if err := request.Encode(&wire); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.DispatchWithMethod(WithUserID(context.Background(), 1), [8]byte{1}, 230, &wire); !tgerr.Is(err, "NOT_IMPLEMENTED") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if logs.FilterMessage("Unhandled RPC (compatibility trace)").Len() != 2 {
		t.Fatalf("wallet trace missing: %+v", logs.All())
	}
}
