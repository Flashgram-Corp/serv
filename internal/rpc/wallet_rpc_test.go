package rpc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"path/filepath"
	"testing"
	"time"

	"github.com/iamxvbaba/td/bin"
	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"github.com/iamxvbaba/td/tlprofile"
	"go.uber.org/zap"

	appaccount "telesrv/internal/app/account"
	walletapp "telesrv/internal/app/wallet"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

type walletRPCFixture struct {
	calls     int
	links     []domain.WalletLink
	created   domain.WalletLink
	createErr error
	replaced  bool
}

func (f *walletRPCFixture) GetProofChallenge(_ context.Context, uid int64, key [8]byte) (domain.WalletChallenge, error) {
	f.calls++
	return domain.WalletChallenge{UserID: uid, AuthKeyID: key, Payload: "challenge", Domain: "wallet.test", ExpiresAt: time.Unix(1800000300, 0)}, nil
}
func (f *walletRPCFixture) Links(context.Context, []int64) ([]domain.WalletLink, error) {
	return f.links, nil
}

func (f *walletRPCFixture) State(context.Context, int64) (domain.WalletLink, int64, bool, error) {
	if len(f.links) == 0 {
		return domain.WalletLink{}, 0, false, nil
	}
	return f.links[0], 4242, true, nil
}

func (f *walletRPCFixture) Create(_ context.Context, uid int64, _ [8]byte, allowReplacement bool) (domain.WalletLink, int64, error) {
	f.calls++
	f.replaced = allowReplacement
	if f.createErr != nil {
		return domain.WalletLink{}, 0, f.createErr
	}
	f.created = domain.WalletLink{UserID: uid, Address: "EQ-created", PublicKey: bytes.Repeat([]byte{3}, 32)}
	return f.created, 4242, nil
}
func (f *walletRPCFixture) ReplaceImported(context.Context, int64, [8]byte, []byte, int, []byte, bool) (domain.WalletLink, int64, error) {
	f.calls++
	return domain.WalletLink{}, 0, domain.ErrWalletProofInvalid
}
func (f *walletRPCFixture) Perform(_ context.Context, endpoint string, query, payload *string) (string, error) {
	f.calls++
	return `{"ok":true,"result":"0"}`, nil
}

// Layer 230 is the canonical profile: the wallet surface is defined there and is
// not part of 225--229, so both dispatch and the encoded result are pinned to
// Profile230 rather than to the canonical alias.
func TestWalletLayer230DispatchAndResultWire(t *testing.T) {
	f := &walletRPCFixture{}
	r := New(Config{}, Deps{Wallet: f}, zap.NewNop(), clock.System)
	key := [8]byte{1}
	ctx := WithAuthKeyID(WithUserID(context.Background(), 42), key)
	for _, tc := range []struct {
		req  bin.Object
		want string
		id   uint32
	}{
		{&tg.WalletGetProofChallengeRequest{}, "wallet.getProofChallenge", 0x99e41707},
		{&tg.ToncenterPerformAPIRequestRequest{Endpoint: "/api/v2/getAddressBalance"}, "toncenter.performApiRequest", 0xac8dfe19},
	} {
		var body bin.Buffer
		if err := tc.req.Encode(&body); err != nil {
			t.Fatal(err)
		}
		admitted, err := r.AdmitLayer(tlprofile.Profile230, &body, tlprofile.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		result, method, err := r.DispatchAdmitted(ctx, key, 1, 0, 1, admitted)
		if err != nil || method != tc.want {
			t.Fatal("layer230 dispatch", method, err)
		}
		var out bin.Buffer
		if err = result.Encode(&out); err != nil {
			t.Fatal(err)
		}
		id, err := out.PeekID()
		if err != nil || id != tc.id {
			t.Fatalf("response ID=%x, error=%v", id, err)
		}
		if _, err = tlprofile.DecodeObject(tlprofile.Profile230, &out, tlprofile.Limits{}); err != nil {
			t.Fatal("response decode", err)
		}
		if out.Len() != 0 {
			t.Fatal("response has trailing bytes")
		}
	}
	if f.calls != 2 {
		t.Fatal("handlers not executed")
	}
}

// The wallet surface belongs to Layer 230 only. An older profile that somehow
// carries the canonical constructor must still fail closed instead of being
// served the canonical result.
func TestWalletHistoricalProfilesRejected(t *testing.T) {
	f := &walletRPCFixture{}
	r := New(Config{}, Deps{Wallet: f}, zap.NewNop(), clock.System)
	var raw bin.Buffer
	if err := (&tg.WalletGetProofChallengeRequest{}).Encode(&raw); err != nil {
		t.Fatal(err)
	}
	wire := raw.Copy()
	for _, profile := range []tlprofile.Profile{
		tlprofile.Profile225, tlprofile.Profile226, tlprofile.Profile227,
		tlprofile.Profile228, tlprofile.Profile229,
	} {
		body := bin.Buffer{Buf: append([]byte(nil), wire...)}
		if _, err := r.AdmitLayer(profile, &body, tlprofile.Limits{}); err == nil {
			t.Fatalf("exact Layer %d admitted the Layer 230 wallet request", profile)
		}
	}
	if f.calls != 0 {
		t.Fatal("rejected historical profile reached the wallet backend")
	}
}

// A router without a wallet service must answer wallet.* with an explicit
// capability error, not a compatibility trace that looks like a missing method.
func TestWalletAbsentServiceReportsUnavailable(t *testing.T) {
	r := New(Config{DC: 2, IP: "127.0.0.1", Port: 2398}, Deps{}, zap.NewNop(), clock.System)
	key := [8]byte{1}
	ctx := WithAuthKeyID(WithUserID(context.Background(), 42), key)
	var wire bin.Buffer
	if err := (&tg.InvokeWithLayerRequest{Layer: 230, Query: &tg.WalletGetProofChallengeRequest{}}).Encode(&wire); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.DispatchWithMethod(ctx, key, 230, &wire); !tgerr.Is(err, "WALLET_UNAVAILABLE") {
		t.Fatalf("absent wallet service error = %v, want WALLET_UNAVAILABLE", err)
	}
	var provider bin.Buffer
	if err := (&tg.InvokeWithLayerRequest{Layer: 230, Query: &tg.ToncenterPerformAPIRequestRequest{Endpoint: "/api/v2/getAddressBalance"}}).Encode(&provider); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.DispatchWithMethod(ctx, key, 230, &provider); !tgerr.Is(err, "WALLET_UNAVAILABLE") {
		t.Fatalf("absent provider error = %v, want WALLET_UNAVAILABLE", err)
	}
}

func TestWalletRejectsUnauthenticated(t *testing.T) {
	f := &walletRPCFixture{}
	r := New(Config{}, Deps{Wallet: f}, zap.NewNop(), clock.System)
	if _, _, err := r.walletCaller(context.Background(), "provider", 120); !tgerr.Is(err, "AUTH_KEY_UNREGISTERED") {
		t.Fatal(err)
	}
	ctx := WithUserID(context.Background(), 42)
	if _, _, err := r.walletCaller(ctx, "provider", 120); !tgerr.Is(err, "AUTH_KEY_UNREGISTERED") {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("unauthenticated wallet request reached backend")
	}
}

func TestWalletRejectsInvalidPeerHash(t *testing.T) {
	f := &walletRPCFixture{}
	users := &mapUsersService{users: map[int64]domain.User{
		42: {ID: 42, AccessHash: 101, FirstName: "Alice"},
		43: {ID: 43, AccessHash: 202, FirstName: "Bob"},
	}}
	r := New(Config{}, Deps{Wallet: f, Users: users}, zap.NewNop(), clock.System)
	ctx := WithAuthKeyID(WithUserID(context.Background(), 42), [8]byte{1})
	for _, hash := range []int64{0, 999} {
		_, err := r.onWalletAddresses(ctx, &tg.WalletGetUserAddressesRequest{ID: []tg.InputUserClass{&tg.InputUser{UserID: 43, AccessHash: hash}}})
		if !tgerr.Is(err, "USER_ID_INVALID") {
			t.Fatalf("hash %d: %v", hash, err)
		}
	}
	if _, err := r.onWalletAddresses(ctx, &tg.WalletGetUserAddressesRequest{ID: []tg.InputUserClass{&tg.InputUser{UserID: 43, AccessHash: 202}}}); err != nil {
		t.Fatal(err)
	}
}

// inputWalletNew asks telesrv to mint the wallet. The first wallet needs no
// password; replacing an existing one does, exactly like an imported swap.
// wallet.getState is the wallet UI's entry point: an unbound account must get
// walletStateEmpty, and a bound one must not claim capabilities telesrv lacks.
func TestWalletStateReportsBindingAndEmpty(t *testing.T) {
	f := &walletRPCFixture{}
	r := New(Config{}, Deps{Wallet: f}, zap.NewNop(), clock.System)
	ctx := WithAuthKeyID(WithUserID(context.Background(), 42), [8]byte{1})

	empty, err := r.onWalletState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty.(*tg.WalletStateEmpty); !ok {
		t.Fatalf("unbound state = %T, want *tg.WalletStateEmpty", empty)
	}

	public := bytes.Repeat([]byte{5}, 32)
	f.links = []domain.WalletLink{{UserID: 42, Address: "EQ-bound", PublicKey: public}}
	bound, err := r.onWalletState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := bound.(*tg.WalletState)
	if !ok || state.Address != "EQ-bound" || state.Balance != 4242 || !bytes.Equal(state.PublicKey, public) {
		t.Fatalf("bound state = %+v", bound)
	}
	if state.Flags != 0 {
		t.Fatalf("state advertises unimplemented capabilities: flags=%#x", state.Flags)
	}
	// The declared constructor of the bound state must be walletState#95c5346b.
	var wire bin.Buffer
	if err = state.Encode(&wire); err != nil {
		t.Fatal(err)
	}
	if id, _ := wire.PeekID(); id != 0x95c5346b {
		t.Fatalf("walletState constructor = %#x, want 0x95c5346b", id)
	}
}

func TestWalletReplaceCreatesServerWallet(t *testing.T) {
	f := &walletRPCFixture{}
	passwords := memory.NewPasswordStore()
	users := &mapUsersService{users: map[int64]domain.User{42: {ID: 42, AccessHash: 101, FirstName: "Alice"}}}
	r := New(Config{}, Deps{
		Wallet:  f,
		Users:   users,
		Account: appaccount.NewService(passwords, appaccount.WithSavedMusic(passwords)),
	}, zap.NewNop(), clock.System)
	ctx := WithAuthKeyID(WithUserID(context.Background(), 42), [8]byte{1})

	result, err := r.onWalletReplace(ctx, &tg.WalletReplaceWalletRequest{Wallet: &tg.InputWalletNew{}})
	if err != nil {
		t.Fatal(err)
	}
	state, ok := result.(*tg.WalletState)
	if !ok || state.Address != f.created.Address || state.Balance != 4242 || len(state.PublicKey) != 32 {
		t.Fatalf("created state = %+v", result)
	}
	if f.replaced {
		t.Fatal("first wallet creation asked for a password check")
	}
	// telesrv must not advertise capabilities it does not implement.
	if state.Flags != 0 {
		t.Fatalf("created state advertises capabilities: flags=%#x", state.Flags)
	}

	// A second wallet over an existing binding is a replacement, so the account
	// password gate must run before anything is minted. An account without a
	// password passes that check trivially, which is exactly Telegram's rule.
	f.links = []domain.WalletLink{{UserID: 42, Address: "EQ-old", PublicKey: bytes.Repeat([]byte{9}, 32)}}
	f.replaced = false
	if _, err = r.onWalletReplace(ctx, &tg.WalletReplaceWalletRequest{Wallet: &tg.InputWalletNew{}}); err != nil {
		t.Fatal(err)
	}
	if !f.replaced {
		t.Fatal("replacing an existing wallet skipped the password gate")
	}
}

func TestWalletReplaceWithoutServerKeyIsUnavailable(t *testing.T) {
	f := &walletRPCFixture{createErr: walletapp.ErrWalletCreationDisabled}
	r := New(Config{}, Deps{Wallet: f}, zap.NewNop(), clock.System)
	ctx := WithAuthKeyID(WithUserID(context.Background(), 42), [8]byte{1})
	if _, err := r.onWalletReplace(ctx, &tg.WalletReplaceWalletRequest{Wallet: &tg.InputWalletNew{}}); !tgerr.Is(err, "WALLET_UNAVAILABLE") {
		t.Fatalf("creation without a server key = %v, want WALLET_UNAVAILABLE", err)
	}
	if _, err := r.onWalletReplace(ctx, &tg.WalletReplaceWalletRequest{Wallet: nil}); !tgerr.Is(err, "WALLET_UNAVAILABLE") {
		t.Fatalf("missing wallet constructor = %v, want WALLET_UNAVAILABLE", err)
	}
}

func TestWalletLayer230OwnershipAndAddressFlow(t *testing.T) {
	store, err := walletapp.NewFileStore(filepath.Join(t.TempDir(), "wallet"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := walletapp.NewService(store, &walletRPCFixture{}, "wallet.test")
	users := &mapUsersService{users: map[int64]domain.User{42: {ID: 42, AccessHash: 101, FirstName: "Alice"}}}
	r := New(Config{}, Deps{Wallet: service, Users: users}, zap.NewNop(), clock.System)
	key := [8]byte{1}
	ctx := WithAuthKeyID(WithUserID(context.Background(), 42), key)
	invoke := func(req bin.Object, out bin.Object) {
		t.Helper()
		var body bin.Buffer
		if err := req.Encode(&body); err != nil {
			t.Fatal(err)
		}
		admitted, err := r.AdmitLayer(tlprofile.Profile230, &body, tlprofile.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		result, _, err := r.DispatchAdmitted(ctx, key, 1, 0, 1, admitted)
		if err != nil {
			t.Fatal(err)
		}
		var encoded bin.Buffer
		if err := result.Encode(&encoded); err != nil {
			t.Fatal(err)
		}
		if err := out.Decode(&encoded); err != nil {
			t.Fatal(err)
		}
		if encoded.Len() != 0 {
			t.Fatal("trailing result bytes")
		}
	}
	var challenge tg.WalletProofChallenge
	invoke(&tg.WalletGetProofChallengeRequest{}, &challenge)
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	public := []byte(private.Public().(ed25519.PublicKey))
	address, err := walletapp.InitialAddress(public, false)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().Unix()
	hash, err := walletapp.ProofHash(0, address.Data(), challenge.Domain, uint64(timestamp), challenge.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var state tg.WalletState
	invoke(&tg.WalletReplaceWalletRequest{Wallet: &tg.InputWalletImported{PublicKey: public, Proof: tg.WalletOwnershipProof{Timestamp: int(timestamp), Signature: ed25519.Sign(private, hash[:])}}}, &state)
	if state.Address != address.String() || !bytes.Equal(state.PublicKey, public) {
		t.Fatal("linked another wallet")
	}
	var addresses tg.WalletUserAddresses
	invoke(&tg.WalletGetUserAddressesRequest{ID: []tg.InputUserClass{&tg.InputUserSelf{}}}, &addresses)
	if len(addresses.Addresses) != 1 || addresses.Addresses[0].UserID != 42 || addresses.Addresses[0].Address != state.Address || len(addresses.Users) != 1 {
		t.Fatalf("wallet lookup mismatch: %+v", addresses)
	}
}
