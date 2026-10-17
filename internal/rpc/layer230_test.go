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

// Layer 230 wallet coverage splits in two, and the split is deliberate.
//
// getNfts stays unregistered: it needs a TON indexer to answer honestly, and a
// traced unknown method is more truthful than one that fabricates a list. TON
// Connect and gasless are registered and refuse instead, because a client polls
// those continuously and every poll used to cost a compatibility trace in the
// log. Refusing is still an honest answer, just a quieter one.
//
// This router carries no Wallet service on purpose: the answers below come from
// the capability gate rather than from a backend, which is also the state a
// deployment with the wallet disabled is in.
func TestLayer230WalletChainMethodsSplitRegisteredFromTraced(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	r := New(Config{DC: 2, IP: "127.0.0.1", Port: 2398}, Deps{}, zap.New(core), clock.System)

	var wire bin.Buffer
	if err := (&tg.InvokeWithLayerRequest{Layer: 230, Query: &tg.WalletGetNftsRequest{}}).Encode(&wire); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.DispatchWithMethod(WithUserID(context.Background(), 1), [8]byte{1}, 230, &wire); !tgerr.Is(err, "NOT_IMPLEMENTED") {
		t.Fatalf("nfts: %v, want NOT_IMPLEMENTED -- it needs a TON indexer and must not fabricate a list", err)
	}

	for name, query := range map[string]bin.Object{
		"gasless":       &tg.WalletGetGaslessInfoRequest{},
		"tonConnect":    &tg.WalletTonConnectGetSessionsRequest{},
		"tonConnectReq": &tg.WalletTonConnectCreateSessionRequest{},
	} {
		wire := bin.Buffer{}
		if err := (&tg.InvokeWithLayerRequest{Layer: 230, Query: query}).Encode(&wire); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.DispatchWithMethod(WithUserID(context.Background(), 1), [8]byte{1}, 230, &wire); !tgerr.Is(err, "WALLET_UNAVAILABLE") {
			t.Fatalf("%s: %v, want WALLET_UNAVAILABLE", name, err)
		}
	}

	// Exactly one trace left: the single method that is genuinely unregistered.
	if got := logs.FilterMessage("Unhandled RPC (compatibility trace)").Len(); got != 1 {
		t.Fatalf("trace count = %d, want 1 (nfts only): %+v", got, logs.All())
	}
}
