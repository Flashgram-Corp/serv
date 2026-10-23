import "errors"
import "messages"
import "types"

const MIN_MINT_VALUE: coins = ton("0.04");

contract CustomFragmentCollection {
    author: "Gramsrv contributors"
    version: "0.1.0"
    description: "Signed mainnet mint authority for Gramsrv collectible gifts"
    storage: CollectionStorage
    incomingMessages: CollectionMessage
}

fun calculateItemState(itemIndex: uint256, collectionAddress: address, itemCode: cell): AutoDeployAddress {
    val data = ItemStorageNotInitialized {
        itemIndex,
        collectionAddress,
    }.toCell();
    return { stateInit: { code: itemCode, data } };
}

fun onInternalMessage(in: InMessage) {
    if (in.body.isEmpty()) {
        return;
    }

    var storage = lazy CollectionStorage.fromCell(contract.getData());
    val msg = lazy CollectionMessage.fromSlice(in.body);
    match (msg) {
        MintNft => {
            val authorization = msg.authorization.load();
            assert (in.senderAddress == authorization.ownerAddress) throw ERROR_UNAUTHORIZED;
            assert (in.valueCoins >= MIN_MINT_VALUE) throw ERROR_NOT_ENOUGH_VALUE;
            assert (authorization.subwalletId == storage.subwalletId) throw ERROR_WRONG_SUBWALLET;
            val now = blockchain.now();
            assert (authorization.validSince <= now && now <= authorization.validTill) throw ERROR_EXPIRED;
            val signedHash = msg.authorization.hash();
            assert (isSignatureValid(signedHash, msg.signature as slice, storage.publicKey)) throw ERROR_UNAUTHORIZED;

            val collectionAddress = contract.getAddress();
            val itemAddress = calculateItemState(authorization.itemIndex, collectionAddress, storage.itemCode);
            val deploy = createMessage({
                bounce: BounceMode.Only256BitsOfBody,
                dest: itemAddress,
                value: 0,
                body: InitializeNft {
                    ownerAddress: authorization.ownerAddress,
                    content: authorization.content,
                },
            });
            deploy.send(SEND_MODE_CARRY_ALL_REMAINING_MESSAGE_VALUE);
            storage.mintedCount += 1;
            contract.setData(storage.toCell());
        }
        RoyaltyParamsRequest => {
            val royalty = storage.royalty.load();
            createMessage({
                bounce: BounceMode.NoBounce,
                dest: in.senderAddress,
                value: 0,
                body: RoyaltyParamsReport {
                    queryId: msg.queryId,
                    numerator: royalty.numerator,
                    denominator: royalty.denominator,
                    destination: royalty.destination,
                },
            }).send(SEND_MODE_CARRY_ALL_REMAINING_MESSAGE_VALUE | SEND_MODE_IGNORE_ERRORS);
        }
        else => {
            throw ERROR_INVALID_MESSAGE;
        }
    }
}

fun onBouncedMessage(_in: InMessageBounced) {
    // Keep returned deployment value in the collection.
}

get fun get_collection_data(): CollectionData {
    val storage = lazy CollectionStorage.fromCell(contract.getData());
    return {
        nextItemIndex: storage.mintedCount,
        collectionContent: storage.collectionContent,
        ownerAddress: null,
    };
}

get fun get_nft_address_by_index(itemIndex: int): address {
    val storage = lazy CollectionStorage.fromCell(contract.getData());
    return calculateItemState(itemIndex, contract.getAddress(), storage.itemCode).calculateAddress();
}

/// Returns the complete TEP-64 content for an item. Gramsrv stores a complete
/// off-chain metadata cell in every item, so the collection must return that
/// cell unchanged. Wallets and indexers call this TEP-62 getter after
/// `get_nft_data`; omitting it makes an otherwise valid NFT appear empty.
get fun get_nft_content(_itemIndex: int, individualContent: cell): cell {
    return individualContent;
}

get fun royalty_params(): RoyaltyParams {
    val storage = lazy CollectionStorage.fromCell(contract.getData());
    return storage.royalty.load();
}
