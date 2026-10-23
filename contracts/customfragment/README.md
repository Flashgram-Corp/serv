# CustomFragment contracts

Tolk NFT collection/item contracts used by gramsrv's CustomFragment Web App.
The collection accepts only short-lived Ed25519-signed mint authorizations
issued by gramsrv and binds every authorization to the connected TON wallet.
Items implement the standard NFT getters and transfer messages.
The collection implements `get_nft_content` and returns each item's complete
off-chain TEP-64 metadata cell unchanged, as required by TEP-62 clients.

This is a typed Tolk rewrite of the minting subset used by Telegram's GPL-3.0
Telemint contracts. It intentionally uses a new storage layout and therefore is
not address-compatible with the original FunC contracts.

```sh
acton build
acton test
acton wrapper
```

Mainnet deployment uses the `customfragment-deployer` Acton wallet and reads
the mint-authority seed from `CUSTOM_FRAGMENT_SIGNING_SEED`; the seed is never
stored in contract data or printed:

```sh
CUSTOM_FRAGMENT_SIGNING_SEED=0x... acton run deploy-mainnet
```
