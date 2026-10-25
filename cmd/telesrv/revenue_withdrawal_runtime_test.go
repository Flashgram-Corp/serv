package main

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestLocalStarGiftWithdrawalRequiresPublicListener(t *testing.T) {
	option, err := localStarGiftWithdrawalOption("not-a-url", "", nil)
	if err != nil || option != nil {
		t.Fatalf("disabled public listener option=%v err=%v, want nil without URL construction", option != nil, err)
	}
	option, err = localStarGiftWithdrawalOption("https://links.example.test", "127.0.0.1:2401", nil)
	if err != nil || option == nil {
		t.Fatalf("enabled public listener option=%v err=%v", option != nil, err)
	}
	if option, err = localStarGiftWithdrawalOption("not-a-url", "127.0.0.1:2401", nil); err == nil || option != nil {
		t.Fatalf("enabled listener accepted dead public URL: option=%v err=%v", option != nil, err)
	}
}

// TestLocalStarGiftWithdrawalSkipsPlainHTTPOutsideLoopback pins gramsrv#135: the
// Docker LAN quick start uses http://<LAN IP>:2401, which must not stop the
// server from starting; it only leaves the bearer-URL withdrawal provider out,
// with a warning. Loopback HTTP keeps it.
func TestLocalStarGiftWithdrawalSkipsPlainHTTPOutsideLoopback(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	option, err := localStarGiftWithdrawalOption("http://192.168.1.20:2401", "0.0.0.0:2401", zap.New(core))
	if err != nil || option != nil {
		t.Fatalf("plain-HTTP LAN option=%v err=%v, want no provider and no startup error", option != nil, err)
	}
	if logs.FilterMessageSnippet("withdrawal disabled").Len() != 1 {
		t.Fatalf("plain-HTTP LAN logs = %v, want one warning", logs.All())
	}
	if option, err = localStarGiftWithdrawalOption("http://127.0.0.1:2401", "127.0.0.1:2401", nil); err != nil || option == nil {
		t.Fatalf("loopback HTTP option=%v err=%v, want the withdrawal provider", option != nil, err)
	}
}
