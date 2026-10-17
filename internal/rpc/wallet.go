package rpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"github.com/iamxvbaba/td/tlprofile"
	"go.uber.org/zap"
	walletapp "telesrv/internal/app/wallet"
	"telesrv/internal/domain"
)

// WalletService 是 deps.go 里声明的应用内 TON 钱包边界；本文件只负责把它
// 映射到 Layer 230 的 wallet.* / toncenter.* 语义方法。
func (r *Router) registerWallet(d *tlprofile.Dispatcher) {
	registerRPC[*tg.WalletGetProofChallengeRequest](d, tlprofile.SemanticMethodWalletGetProofChallenge, func(ctx context.Context, _ *tg.WalletGetProofChallengeRequest) (any, error) {
		uid, key, err := r.walletCaller(ctx, "challenge", 60)
		if err != nil {
			return nil, err
		}
		c, err := r.deps.Wallet.GetProofChallenge(ctx, uid, key)
		if err != nil {
			return nil, walletRPCError(err)
		}
		return &tg.WalletProofChallenge{Payload: c.Payload, Expires: int(c.ExpiresAt.Unix()), Domain: c.Domain}, nil
	})
	registerRPC[*tg.WalletReplaceWalletRequest](d, tlprofile.SemanticMethodWalletReplaceWallet, func(ctx context.Context, req *tg.WalletReplaceWalletRequest) (any, error) {
		return r.onWalletReplace(ctx, req)
	})
	registerRPC[*tg.WalletGetStateRequest](d, tlprofile.SemanticMethodWalletGetState, func(ctx context.Context, _ *tg.WalletGetStateRequest) (any, error) {
		return r.onWalletState(ctx)
	})
	registerRPC[*tg.WalletGetExistingWaltBalanceRequest](d, tlprofile.SemanticMethodWalletGetExistingWaltBalance, func(ctx context.Context, _ *tg.WalletGetExistingWaltBalanceRequest) (any, error) {
		// telesrv holds balances in its own ledger and configures no external
		// TON provider, so there is no on-chain balance page to send the client
		// to. Reporting has_balance=false is the honest answer.
		return &tg.WalletExistingBalance{}, nil
	})
	registerRPC[*tg.WalletGetUserAddressesRequest](d, tlprofile.SemanticMethodWalletGetUserAddresses, func(ctx context.Context, req *tg.WalletGetUserAddressesRequest) (any, error) {
		return r.onWalletAddresses(ctx, req)
	})
	registerRPC[*tg.ToncenterPerformAPIRequestRequest](d, tlprofile.SemanticMethodToncenterPerformAPIRequest, func(ctx context.Context, req *tg.ToncenterPerformAPIRequestRequest) (any, error) {
		if _, _, err := r.walletCaller(ctx, "provider", 120); err != nil {
			return nil, err
		}
		var query, payload *string
		if v, ok := req.GetQuery(); ok {
			query = &v
		}
		if v, ok := req.GetPayload(); ok {
			payload = &v
		}
		result, err := r.deps.Wallet.Perform(ctx, req.Endpoint, query, payload)
		if err != nil {
			return nil, walletRPCError(err)
		}
		return &tg.ToncenterAPIResponse{Response: tg.DataJSON{Data: result}}, nil
	})
}

func (r *Router) walletCaller(ctx context.Context, scope string, limit int) (int64, [8]byte, error) {
	uid, ok, err := r.currentUserID(ctx)
	if err != nil {
		return 0, [8]byte{}, internalErr()
	}
	if !ok || uid <= 0 {
		return 0, [8]byte{}, authKeyUnregisteredErr()
	}
	key, ok := AuthKeyIDFrom(ctx)
	if !ok || key == ([8]byte{}) {
		return 0, [8]byte{}, authKeyUnregisteredErr()
	}
	if r.deps.Wallet == nil {
		return 0, key, tgerr.New(400, "WALLET_UNAVAILABLE")
	}
	if r.deps.Limiter != nil {
		allowed, retry, err := r.deps.Limiter.AllowN(ctx, walletRateLimitKey(scope, uid, key), 1, limit, time.Minute)
		if err != nil {
			return 0, key, internalErr()
		}
		if !allowed {
			return 0, key, floodWaitErr(retry)
		}
	}
	return uid, key, nil
}

func walletRateLimitKey(scope string, userID int64, key [8]byte) string {
	return "wallet:" + scope + ":" + strconv.FormatInt(userID, 10) + ":" + hex.EncodeToString(key[:])
}

func (r *Router) onWalletReplace(ctx context.Context, req *tg.WalletReplaceWalletRequest) (any, error) {
	uid, key, err := r.walletCaller(ctx, "replace", 10)
	if err != nil {
		return nil, err
	}
	// inputWalletNew asks telesrv to mint the wallet: no client-side secret is
	// involved, so the account password is what protects an existing binding.
	// inputWalletImported carries an Ed25519 ownership proof instead.
	var imported *tg.InputWalletImported
	create := false
	switch wallet := req.Wallet.(type) {
	case *tg.InputWalletImported:
		imported = wallet
	case *tg.InputWalletNew:
		create = true
	default:
		return nil, tgerr.New(400, "WALLET_UNAVAILABLE")
	}
	links, err := r.deps.Wallet.Links(ctx, []int64{uid})
	if err != nil {
		return nil, walletRPCError(err)
	}
	password, provided := req.GetPassword()
	needsReplacement := len(links) > 0 && (create || !bytes.Equal(links[0].PublicKey, imported.PublicKey))
	allowReplacement := false
	if provided || needsReplacement {
		if r.deps.Account == nil {
			return nil, internalErr()
		}
		if err = r.deps.Account.CheckPassword(ctx, uid, domainPasswordCheck(password)); err != nil {
			return nil, passwordErr(err)
		}
		allowReplacement = true
	}
	var link domain.WalletLink
	var balance int64
	if create {
		link, balance, err = r.deps.Wallet.Create(ctx, uid, key, allowReplacement)
	} else {
		link, balance, err = r.deps.Wallet.ReplaceImported(ctx, uid, key, imported.PublicKey, imported.Proof.Timestamp, imported.Proof.Signature, allowReplacement)
	}
	if err != nil {
		return nil, walletRPCError(err)
	}
	// Backup, phrase export and gasless are server capabilities telesrv does not
	// implement yet. Advertising them would make clients offer flows that fail.
	return &tg.WalletState{Address: link.Address, PublicKey: link.PublicKey, Balance: balance}, nil
}

// onWalletState answers wallet.getState. An account without a wallet gets
// walletStateEmpty, which is how the client renders "create a wallet"; telesrv
// never invents an address, and it never advertises backup, phrase export or
// enable-backup capabilities it does not implement.
func (r *Router) onWalletState(ctx context.Context) (any, error) {
	uid, _, err := r.walletCaller(ctx, "state", 60)
	if err != nil {
		return nil, err
	}
	link, balance, ok, err := r.deps.Wallet.State(ctx, uid)
	if err != nil {
		return nil, walletRPCError(err)
	}
	if !ok {
		return &tg.WalletStateEmpty{}, nil
	}
	return &tg.WalletState{Address: link.Address, PublicKey: link.PublicKey, Balance: balance}, nil
}

func (r *Router) onWalletAddresses(ctx context.Context, req *tg.WalletGetUserAddressesRequest) (any, error) {
	uid, _, err := r.walletCaller(ctx, "addresses", 60)
	if err != nil {
		return nil, err
	}
	if req.Flags != 0 || len(req.ID) > 100 || len(req.Addresses) > 100 {
		return nil, inputRequestInvalidErr()
	}
	if r.deps.Users == nil {
		return nil, internalErr()
	}
	ids := make([]int64, 0, len(req.ID))
	validInputs := make([]tg.InputUserClass, 0, len(req.ID))
	for _, input := range req.ID {
		// An arbitrary bare ID must not bypass peer access-hash validation.
		if v, ok := input.(*tg.InputUser); ok && v.UserID != uid && v.AccessHash == 0 {
			return nil, userIDInvalidErr()
		}
		u, found, err := r.userFromInput(ctx, uid, input)
		if err != nil {
			return nil, internalErr()
		}
		if !found {
			return nil, userIDInvalidErr()
		}
		ids = append(ids, u.ID)
		validInputs = append(validInputs, input)
	}
	// Address-only lookup is restricted to the caller's own association. It
	// must not become an account-enumeration endpoint for arbitrary TON addresses.
	if len(ids) == 0 && len(req.Addresses) > 0 {
		ids = append(ids, uid)
		validInputs = append(validInputs, &tg.InputUserSelf{})
	}
	links, err := r.deps.Wallet.Links(ctx, ids)
	if err != nil {
		return nil, walletRPCError(err)
	}
	result := &tg.WalletUserAddresses{Addresses: []tg.WalletUserAddress{}, Users: []tg.UserClass{}}
	for _, link := range links {
		if len(req.ID) == 0 {
			match := false
			for _, a := range req.Addresses {
				if a == link.Address {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		result.Addresses = append(result.Addresses, tg.WalletUserAddress{UserID: link.UserID, Address: link.Address, PublicKey: link.PublicKey})
	}
	result.Users, err = r.onUsersGetUsers(ctx, validInputs)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func walletRPCError(err error) error {
	switch {
	case errors.Is(err, domain.ErrWalletProofInvalid):
		return tgerr.New(400, "WALLET_PROOF_INVALID")
	case errors.Is(err, domain.ErrWalletPasswordRequired):
		return tgerr.New(400, "PASSWORD_HASH_INVALID")
	case errors.Is(err, walletapp.ErrWalletCreationDisabled), errors.Is(err, walletapp.ErrWalletKeyFile):
		// telesrv 没法保管私钥（未配置主密钥）。这是能力不可用，不是内部错误。
		return tgerr.New(400, "WALLET_UNAVAILABLE")
	case errors.Is(err, walletapp.ErrProviderRequest):
		return inputRequestInvalidErr()
	case errors.Is(err, walletapp.ErrProviderResponse):
		return tgerr.New(502, "WALLET_PROVIDER_RESPONSE_INVALID")
	case errors.Is(err, walletapp.ErrProviderUnavailable):
		return tgerr.New(503, "WALLET_PROVIDER_UNAVAILABLE")
	}
	var httpErr *walletapp.ProviderHTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Status == 429 {
			return floodWaitErr(1)
		}
		return tgerr.New(503, "WALLET_PROVIDER_UNAVAILABLE")
	}
	return internalErr()
}

// Trace only routing metadata, never challenge, proof, provider payload or key.
func (r *Router) walletTraceFields(ctx context.Context, method string) []zap.Field {
	if r.log == nil {
		return nil
	}
	switch method {
	case "wallet.getProofChallenge", "wallet.replaceWallet", "wallet.getUserAddresses", "toncenter.performApiRequest":
		uid, _ := UserIDFrom(ctx)
		return []zap.Field{zap.String("method", method), zap.Int64("user_id", uid)}
	default:
		return nil
	}
}
