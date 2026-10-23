import "errors"
import "messages"
import "types"

contract CustomFragmentItem {
    author: "Gramsrv contributors"
    version: "0.1.0"
    description: "TEP-62 item for a Gramsrv collectible gift"
    storage: ItemStorageInitialized
    storageAtDeployment: ItemStorageNotInitialized
    incomingMessages: ItemMessage
}

fun onInternalMessage(in: InMessage) {
    var loading = ItemStorageInitialized.startLoading();
    if (loading.isNotInitialized()) {
        assert (in.senderAddress == loading.collectionAddress) throw ERROR_UNAUTHORIZED;
        val init = lazy InitializeNft.fromSlice(in.body);
        contract.setData(ItemStorageInitialized {
            itemIndex: loading.itemIndex,
            collectionAddress: loading.collectionAddress,
            ownerAddress: init.ownerAddress,
            content: init.content,
        }.toCell());
        return;
    }

    var storage = loading.endLoading();
    val msg = lazy ItemMessage.fromSlice(in.body);
    match (msg) {
        TransferNft => {
            assert (in.senderAddress == storage.ownerAddress) throw ERROR_UNAUTHORIZED;
            storage.ownerAddress = msg.newOwner;
            contract.setData(storage.toCell());

            if (msg.forwardAmount > 0) {
                createMessage({
                    bounce: BounceMode.NoBounce,
                    dest: msg.newOwner,
                    value: msg.forwardAmount,
                    body: OwnershipAssigned {
                        queryId: msg.queryId,
                        previousOwner: in.senderAddress,
                        forwardPayload: msg.forwardPayload,
                    },
                }).send(SEND_MODE_PAY_FEES_SEPARATELY | SEND_MODE_IGNORE_ERRORS);
            }
            createMessage({
                bounce: BounceMode.NoBounce,
                dest: msg.responseDestination,
                value: 0,
                body: Excesses { queryId: msg.queryId },
            }).send(SEND_MODE_CARRY_ALL_REMAINING_MESSAGE_VALUE | SEND_MODE_IGNORE_ERRORS);
        }
        StaticDataRequest => {
            createMessage({
                bounce: BounceMode.NoBounce,
                dest: in.senderAddress,
                value: 0,
                body: StaticDataReport {
                    queryId: msg.queryId,
                    itemIndex: storage.itemIndex,
                    collectionAddress: storage.collectionAddress,
                },
            }).send(SEND_MODE_CARRY_ALL_REMAINING_MESSAGE_VALUE | SEND_MODE_IGNORE_ERRORS);
        }
        else => {
            throw ERROR_INVALID_MESSAGE;
        }
    }
}

fun onBouncedMessage(_in: InMessageBounced) {
    // NFT state is never changed by bounced notifications.
}

get fun get_nft_data(): NftData {
    var loading = ItemStorageInitialized.startLoading();
    if (loading.isNotInitialized()) {
        return {
            initialized: 0,
            itemIndex: loading.itemIndex,
            collectionAddress: loading.collectionAddress,
            ownerAddress: null,
            content: null,
        };
    }
    val storage = loading.endLoading();
    return {
        initialized: -1,
        itemIndex: storage.itemIndex,
        collectionAddress: storage.collectionAddress,
        ownerAddress: storage.ownerAddress,
        content: storage.content,
    };
}
