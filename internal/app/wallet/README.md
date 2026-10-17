# Wallet server integration

telesrv implements the Layer 230 in-app TON wallet surface: ownership-checked
account associations, server-held wallets, and telesrv's own TON ledger as the
balance source. Everything runs against the built-in backend; no TON node,
Fragment endpoint or third-party provider is contacted unless one is
configured.

The wallet RPCs are canonical Layer 230 constructors served by
`github.com/iamxvbaba/td` v1.4.1. Profiles 225--229 do not carry them, and
telesrv never aliases a wallet request onto an older profile.

## Registered RPCs

- `wallet.getProofChallenge`: unpredictable challenge bound to account and
  MTProto authorization, server domain, five-minute expiry.
- `wallet.replaceWallet`: accepts both wallet constructors.
  - `inputWalletImported` verifies an Ed25519 ownership proof against the
    challenge and stores the address association.
  - `inputWalletNew` mints the wallet here: telesrv generates the Ed25519 pair,
    derives the mainnet address, and keeps the private key sealed under the
    wallet master key. The client receives only address, public key and
    balance. Replacing an existing wallet still requires the account password
    (SRP), and an identical retry inside the challenge window returns the same
    wallet instead of minting a second one.
- `wallet.getState`: current address, public key and local-ledger balance, or
  `walletStateEmpty` when the account has no wallet. Backup, phrase export and
  enable-backup flags are never advertised, because telesrv does not implement
  them.
- `wallet.getUserAddresses`: validated InputUser access hashes, stored
  addresses and projected Telegram user entities. Address-only lookup is
  limited to self.
- `wallet.getExistingWaltBalance`: reports `has_balance=false`, since telesrv
  configures no external TON balance page.
- `toncenter.performApiRequest`: bounded HTTPS JSON relay to toncenter.com,
  canonical /api/v2/ and /api/v3/ paths, no redirects or application retries.
  JSON provider errors are preserved. Without `TELESRV_WALLET_TONCENTER_API_KEY`
  the RPC answers `WALLET_PROVIDER_UNAVAILABLE` and nothing leaves the process.

Still unimplemented, and deliberately traced rather than faked:
`wallet.getTransactions*`, `wallet.sendTransfer`, `wallet.getNfts`,
`wallet.getGaslessInfo`, the TON Connect family, phrase export/backup RPCs and
the onramp/currency methods.

Initial address derivation uses the anchor key. A rotated wallet's address is
not inferred from its current signing key alone.

## Balances

Balances come from telesrv's internal TON ledger (`ton_balances`, nanoton), the
same ledger that credits star-gift proceeds. Its first-read grant is
`TELESRV_STARGIFT_TON_STARTING_GRANT`. Wallet code only reads it; moving funds
between accounts is not implemented yet, so no ledger row is written by the
wallet surface.

## Persistence and configuration

`TELESRV_WALLET_STATE_DIR` selects a private directory; the default is
`data/wallet` relative to the service working directory. The directory must have
no group/other permissions. Per-user JSON files are mode 0600. A process lock
allows one writer; each mutation syncs the file, renames it atomically, and
syncs the directory. Challenge consumption and wallet replacement share one
record. Corrupt records fail closed.

`TELESRV_WALLET_KEY_FILE` (default `data/wallet/keys.key`) is the AES-256-GCM
master key for wallets telesrv generated. It is created 0600 on first boot and
rejects group/world-readable files. Without it telesrv cannot custody a key, so
`inputWalletNew` is refused instead of storing anything unsealed. Imported
wallets never have private key material on the server at all.

`TELESRV_WALLET_TONCENTER_API_KEY` optionally supplies the provider API key. The
proof domain uses the server's configured advertise IP. Never log provider keys,
proofs, wallet keys, export tokens, or recovery material.

There are no wallet PostgreSQL migrations or test databases required: identity
is file-backed, balances live in the existing TON ledger tables.

## Protocol reference and cryptography

The source reference is `Telegram-iOS-wallet/docs/wallet/MTProto-Wallet-Reference.ru.md`
and its `methods.tl` / `responses.tl`. It describes 25 request signatures and
refined backup, TON Connect, transaction and update response layouts. In
particular, secretPhraseParts.dcs is Vector<int>, proofChallenge.payload is a
string, and TonConnectSession optional fields use bits 3, 4 and 5.

`internal/walletbackup` implements the documented client backup crypto: three
XOR shares, Ed25519-to-X25519 conversion, TDLib ECDH/HMAC key derivation,
authenticated AES-CBC envelopes and strict indexed/bare recovery framing. Known
answer fixtures use an independent Python cryptography/OpenSSL implementation.
This crypto component is separate from export-token authorization and holder
server operations. The document distinguishes known wire fields from server
semantics and live compatibility evidence; preserve that distinction when
extending the registered RPCs.

## Validation

```
go test -race ./internal/app/wallet ./internal/walletbackup -count=1
go test ./internal/rpc -run 'TestWallet|TestLayer230' -count=1
env -u TELESRV_TEST_POSTGRES_DSN -u TELESRV_RUN_COMPOSE_INTEGRATION go test ./... -count=1
go vet ./...
```

Tests cover cryptographic vectors, malformed envelopes, provider validation,
proof binding/expiry, failed-provider non-mutation, restart persistence,
idempotent retries, competing initial claims, store locking/corruption, master
key round-trip and isolation, server-held creation and its retry, refusal
without a master key, layer-230 dispatch/result decoding and historical-profile
rejection. These tests do not send real funds or prove an end-to-end
device/blockchain transfer or Telegram backup lifecycle.