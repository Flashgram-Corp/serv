package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/iamxvbaba/td/bin"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"github.com/iamxvbaba/td/tlprofile"

	appaccount "telesrv/internal/app/account"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

type withdrawalRPCGifts struct {
	GiftsService
	uniques map[string]domain.UniqueStarGift
	info    domain.StarGiftValueInfo
	req     domain.StarGiftWithdrawalRequest
	calls   int
	err     error
}

func (s *withdrawalRPCGifts) UniqueBySlug(_ context.Context, slug string) (domain.UniqueStarGift, bool, error) {
	gift, found := s.uniques[slug]
	return gift, found, nil
}

func (s *withdrawalRPCGifts) ValueInfo(context.Context, int64) (domain.StarGiftValueInfo, error) {
	return s.info, nil
}

func (s *withdrawalRPCGifts) Withdraw(_ context.Context, req domain.StarGiftWithdrawalRequest) (domain.StarGiftWithdrawal, error) {
	s.calls++
	s.req = req
	if s.err != nil {
		return domain.StarGiftWithdrawal{}, s.err
	}
	return domain.StarGiftWithdrawal{URL: "https://links.example.test/gift-withdrawal/token"}, nil
}

// withdrawalRPCAccount stands in for a future account.getTmpPassword issuer so
// the later admission steps stay covered. Production fails closed at
// CheckTmpPassword; TestStarGiftWithdrawalTmpPasswordFailsClosed pins that.
type withdrawalRPCAccount struct {
	AccountService
	checks   int
	checked  []byte
	checkErr error
	state    domain.RevenueWithdrawalPasswordState
}

func (s *withdrawalRPCAccount) CheckTmpPassword(_ context.Context, _ int64, tmpPassword []byte) error {
	s.checks++
	s.checked = append([]byte(nil), tmpPassword...)
	return s.checkErr
}

func (s *withdrawalRPCAccount) RevenueWithdrawalPasswordState(context.Context, int64) (domain.RevenueWithdrawalPasswordState, error) {
	return s.state, nil
}

type withdrawalRPCAuth struct {
	AuthService
	authorization domain.Authorization
}

func (s *withdrawalRPCAuth) Authorization(context.Context, [8]byte) (domain.Authorization, bool, error) {
	return s.authorization, true, nil
}

func starGiftWithdrawalRouter(t *testing.T) (
	*Router,
	int64,
	*withdrawalRPCGifts,
	*withdrawalRPCAccount,
	*withdrawalRPCAuth,
	[8]byte) {
	t.Helper()
	r, sender, _, _ := starGiftTestRouter(t)
	now := time.Now()
	authKeyID := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	gifts := &withdrawalRPCGifts{
		uniques: map[string]domain.UniqueStarGift{
			"owned-by-someone-else": {ID: 71, Slug: "owned-by-someone-else",
				Owner: domain.Peer{Type: domain.PeerTypeChannel, ID: 4242}},
			"withdrawal-test-gift": {ID: 72, Slug: "withdrawal-test-gift",
				Owner: domain.Peer{Type: domain.PeerTypeUser, ID: sender.ID}},
		},
		info: domain.StarGiftValueInfo{Currency: "TON", Value: 1200000000},
	}
	account := &withdrawalRPCAccount{state: domain.RevenueWithdrawalPasswordState{
		HasPassword: true, PasswordChangedAt: now.Add(-48 * time.Hour),
	}}
	auth := &withdrawalRPCAuth{authorization: domain.Authorization{
		AuthKeyID: authKeyID, UserID: sender.ID, CreatedAt: now.Add(-48 * time.Hour),
	}}
	r.deps.Gifts = gifts
	r.deps.Account = account
	r.deps.Auth = auth
	return r, sender.ID, gifts, account, auth, authKeyID
}

func withdrawalRequest(slug string, tmpPassword []byte) *tg.PaymentsGetStarGiftWithdrawalURLRequest {
	return &tg.PaymentsGetStarGiftWithdrawalURLRequest{
		Stargift:    &tg.InputSavedStarGiftSlug{Slug: slug},
		TmpPassword: tmpPassword,
	}
}

func TestStarGiftWithdrawalNeedsMatchingOwnerAndKnownSlug(t *testing.T) {
	r, userID, gifts, _, _, authKeyID := starGiftWithdrawalRouter(t)
	ctx := WithAuthKeyID(WithUserID(context.Background(), userID), authKeyID)

	if _, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(
		"owned-by-someone-else", withdrawalTmpPassword(1))); !tgerr.Is(err, "STARGIFT_OWNER_INVALID") {
		t.Fatalf("foreign owner err = %v, want STARGIFT_OWNER_INVALID", err)
	}
	if gifts.calls != 0 {
		t.Fatalf("foreign owner still withdrew: calls=%d", gifts.calls)
	}

	if _, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(
		"never-minted", withdrawalTmpPassword(1))); !tgerr.Is(err, "STARGIFT_INVALID") {
		t.Fatalf("unknown slug err = %v, want STARGIFT_INVALID", err)
	}
	if gifts.calls != 0 {
		t.Fatalf("unknown slug still withdrew: calls=%d", gifts.calls)
	}

	got, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(
		"withdrawal-test-gift", withdrawalTmpPassword(9)))
	if err != nil {
		t.Fatalf("owner withdrawal: %v", err)
	}
	if got.URL == "" || gifts.calls != 1 || gifts.req.UserID != userID || gifts.req.Ref.Slug != "withdrawal-test-gift" ||
		gifts.req.Ref.Owner != (domain.Peer{Type: domain.PeerTypeUser, ID: userID}) {
		t.Fatalf("withdrawal = %+v req = %+v", got, gifts.req)
	}
}

func TestStarGiftWithdrawalRejectsUnproven2FAAndFreshSessions(t *testing.T) {
	r, userID, gifts, account, auth, authKeyID := starGiftWithdrawalRouter(t)
	ctx := WithAuthKeyID(WithUserID(context.Background(), userID), authKeyID)
	slug := "withdrawal-test-gift"

	account.checkErr = domain.ErrTmpPasswordInvalid
	if _, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(
		slug, withdrawalTmpPassword(9))); !tgerr.Is(err, "TMP_PASSWORD_INVALID") {
		t.Fatalf("wrong tmp password err = %v, want TMP_PASSWORD_INVALID", err)
	}
	if gifts.calls != 0 || !bytes.Equal(account.checked, withdrawalTmpPassword(9)) {
		t.Fatalf("wrong tmp password: calls=%d checked=%x", gifts.calls, account.checked)
	}
	account.checkErr = nil

	// An empty tmp_password is no proof: a passwordless account no longer
	// passes the way inputCheckPasswordEmpty used to, with or without bytes.
	account.state = domain.RevenueWithdrawalPasswordState{}
	account.checks = 0
	for _, tmp := range [][]byte{nil, withdrawalTmpPassword(9)} {
		if _, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(slug, tmp)); !tgerr.Is(err, "PASSWORD_MISSING") {
			t.Fatalf("passwordless tmp %x err = %v, want PASSWORD_MISSING", tmp, err)
		}
	}
	if account.checks != 0 || gifts.calls != 0 {
		t.Fatalf("passwordless account reached tmp check or withdrew: checks=%d calls=%d", account.checks, gifts.calls)
	}

	account.state = domain.RevenueWithdrawalPasswordState{
		HasPassword: true, PasswordChangedAt: time.Now().Add(-time.Hour),
	}
	_, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(
		slug, withdrawalTmpPassword(9)))
	if !tgerr.Is(err, "PASSWORD_TOO_FRESH") {
		t.Fatalf("fresh password err = %v, want PASSWORD_TOO_FRESH", err)
	}
	rpcErr, ok := tgerr.As(err)
	if !ok || rpcErr.Argument <= 0 || rpcErr.Argument > 24*60*60 {
		t.Fatalf("PASSWORD_TOO_FRESH wait = %+v", rpcErr)
	}

	account.state = domain.RevenueWithdrawalPasswordState{
		HasPassword: true, PasswordChangedAt: time.Now().Add(-48 * time.Hour),
	}
	auth.authorization.CreatedAt = time.Now().Add(-time.Minute)
	_, err = r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(
		slug, withdrawalTmpPassword(9)))
	if !tgerr.Is(err, "SESSION_TOO_FRESH") {
		t.Fatalf("fresh session err = %v, want SESSION_TOO_FRESH", err)
	}
	rpcErr, ok = tgerr.As(err)
	if !ok || rpcErr.Argument <= 0 || rpcErr.Argument > 24*60*60 {
		t.Fatalf("SESSION_TOO_FRESH wait = %+v", rpcErr)
	}
}

func TestStarGiftWithdrawalRejectsBotsAndMapsCooldown(t *testing.T) {
	r, userID, gifts, _, _, authKeyID := starGiftWithdrawalRouter(t)
	ctx := WithAuthKeyID(WithUserID(context.Background(), userID), authKeyID)
	slug := "withdrawal-test-gift"
	tmp := withdrawalTmpPassword(9)

	r.botStatus.Store(userID, true)
	if _, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(slug, tmp)); !tgerr.Is(err, "BOT_METHOD_INVALID") {
		t.Fatalf("bot err = %v, want BOT_METHOD_INVALID", err)
	}
	if gifts.calls != 0 {
		t.Fatalf("bot still withdrew: calls=%d", gifts.calls)
	}
	r.botStatus.Delete(userID)

	gifts.err = domain.ErrStarGiftExportCooldown
	if _, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(slug, tmp)); !tgerr.Is(err, "STARGIFT_WITHDRAWAL_UNAVAILABLE") {
		t.Fatalf("cooldown err = %v, want STARGIFT_WITHDRAWAL_UNAVAILABLE", err)
	}
	gifts.err = nil

	gifts.err = domain.ErrStarGiftTransferUnavailable
	if _, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(slug, tmp)); !tgerr.Is(err, "STARGIFT_INVALID") {
		t.Fatalf("closed export err = %v, want STARGIFT_INVALID", err)
	}
}

// withdrawalRPCPasswordState keeps the production account service and fakes
// only the durable 2FA facts, so CheckTmpPassword is the real gate.
type withdrawalRPCPasswordState struct {
	AccountService
	state domain.RevenueWithdrawalPasswordState
}

func (s *withdrawalRPCPasswordState) RevenueWithdrawalPasswordState(context.Context, int64) (domain.RevenueWithdrawalPasswordState, error) {
	return s.state, nil
}

func TestStarGiftWithdrawalTmpPasswordFailsClosed(t *testing.T) {
	r, userID, gifts, _, _, authKeyID := starGiftWithdrawalRouter(t)
	ctx := WithAuthKeyID(WithUserID(context.Background(), userID), authKeyID)
	r.deps.Account = &withdrawalRPCPasswordState{
		AccountService: appaccount.NewService(memory.NewPasswordStore()),
		state: domain.RevenueWithdrawalPasswordState{
			HasPassword: true, PasswordChangedAt: time.Now().Add(-48 * time.Hour),
		},
	}
	// telesrv issues no account.getTmpPassword value: neither an empty nor an
	// arbitrary tmp_password may reach the slug, freshness or export checks.
	for _, slug := range []string{"withdrawal-test-gift", "never-minted"} {
		for _, tmp := range [][]byte{nil, {}, withdrawalTmpPassword(9)} {
			if _, err := r.onPaymentsGetStarGiftWithdrawalURL(ctx, withdrawalRequest(slug, tmp)); !tgerr.Is(err, "TMP_PASSWORD_INVALID") {
				t.Fatalf("%s tmp %x err = %v, want TMP_PASSWORD_INVALID", slug, tmp, err)
			}
		}
	}
	if gifts.calls != 0 {
		t.Fatalf("unverifiable tmp_password withdrew: calls=%d", gifts.calls)
	}
}

func layer231WithdrawalWire(password bin.Encoder) []byte {
	// payments.getStarGiftWithdrawalUrl#d06e93a8 stargift:InputSavedStarGift
	// password:InputCheckPasswordSRP, as every 225-231 client still sends it.
	var b bin.Buffer
	b.PutID(0xd06e93a8)
	if err := (&tg.InputSavedStarGiftSlug{Slug: "withdrawal-test-gift"}).Encode(&b); err != nil {
		panic(err)
	}
	if err := password.Encode(&b); err != nil {
		panic(err)
	}
	return b.Copy()
}

func TestStarGiftWithdrawalExactLayerAdmission(t *testing.T) {
	r, _, _, _, _, _ := starGiftWithdrawalRouter(t)
	request := withdrawalRequest("withdrawal-test-gift", withdrawalTmpPassword(9))
	wire := encodeExactLayerRPC(t, tlprofile.Profile232, request)
	if id := binary.LittleEndian.Uint32(wire.Buf); id != 0xc8ca817b {
		t.Fatalf("Layer 232 withdrawal wire id = %#x", id)
	}
	decodeWire := bin.Buffer{Buf: append([]byte(nil), wire.Buf...)}
	decoded, err := tlprofile.DecodeObject(tlprofile.Profile232, &decodeWire, tlprofile.Limits{})
	if got, ok := decoded.(*tg.PaymentsGetStarGiftWithdrawalURLRequest); err != nil || !ok ||
		!bytes.Equal(got.TmpPassword, request.TmpPassword) {
		t.Fatalf("Layer 232 withdrawal decoded = %#v, %v", decoded, err)
	}
	admitted, err := r.AdmitLayer(tlprofile.Profile232, &wire, tlprofile.Limits{})
	if err != nil {
		t.Fatalf("admit Layer 232 withdrawal: %v", err)
	}
	if _, method, _ := tlprofile.SemanticName(admitted.Call().Method()); method != "payments.getStarGiftWithdrawalUrl" ||
		!r.dispatcher.Has(admitted.Call().Method()) {
		t.Fatalf("Layer 232 withdrawal admitted as %q, registered=%v", method, r.dispatcher.Has(admitted.Call().Method()))
	}

	// td rejects the historical SRP constructor at admission instead of
	// dropping the proof. It surfaces as ErrUnknownRPCMethod, which the edge
	// logs as "Unhandled RPC admission (compatibility trace)" and answers
	// NOT_IMPLEMENTED; no handler can see it as an empty tmp_password.
	for profile := tlprofile.Profile225; profile <= tlprofile.Profile231; profile++ {
		for _, password := range []bin.Encoder{
			&tg.InputCheckPasswordSRP{SRPID: 1, A: withdrawalTmpPassword(2), M1: withdrawalTmpPassword(3)},
			&tg.InputCheckPasswordEmpty{},
		} {
			raw := layer231WithdrawalWire(password)
			legacy := bin.Buffer{Buf: append([]byte(nil), raw...)}
			if _, err := r.AdmitLayer(profile, &legacy, tlprofile.Limits{}); !errors.Is(err, tlprofile.ErrUnknownRPCMethod) ||
				!bytes.Equal(legacy.Raw(), raw) {
				t.Fatalf("Layer %d %T withdrawal admission err = %v (input consumed=%v), want ErrUnknownRPCMethod",
					profile, password, err, !bytes.Equal(legacy.Raw(), raw))
			}
		}
	}
}

func TestUniqueStarGiftValueInfoUsesSlugError(t *testing.T) {
	r, userID, gifts, _, _, authKeyID := starGiftWithdrawalRouter(t)
	ctx := WithAuthKeyID(WithUserID(context.Background(), userID), authKeyID)

	if _, err := r.onPaymentsGetUniqueStarGiftValueInfo(ctx,
		&tg.PaymentsGetUniqueStarGiftValueInfoRequest{Slug: "never-minted"}); !tgerr.Is(err, "STARGIFT_SLUG_INVALID") {
		t.Fatalf("unknown slug err = %v, want STARGIFT_SLUG_INVALID", err)
	}

	got, err := r.onPaymentsGetUniqueStarGiftValueInfo(ctx,
		&tg.PaymentsGetUniqueStarGiftValueInfoRequest{Slug: " WITHDRAWAL-TEST-GIFT "})
	if err != nil || got.Currency != "TON" || got.Value != gifts.info.Value {
		t.Fatalf("value info = %+v err = %v", got, err)
	}

	r.botStatus.Store(userID, true)
	if _, err := r.onPaymentsGetUniqueStarGiftValueInfo(ctx,
		&tg.PaymentsGetUniqueStarGiftValueInfoRequest{Slug: "withdrawal-test-gift"}); !tgerr.Is(err, "BOT_METHOD_INVALID") {
		t.Fatalf("bot value info err = %v, want BOT_METHOD_INVALID", err)
	}
}

func withdrawalTmpPassword(fill byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = fill
	}
	return out
}
