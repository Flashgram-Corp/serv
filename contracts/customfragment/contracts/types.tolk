struct RoyaltyParams {
    numerator: uint16,
    denominator: uint16,
    destination: address,
}

struct CollectionStorage {
    mintedCount: uint64,
    subwalletId: uint32,
    publicKey: uint256,
    collectionContent: cell,
    itemCode: cell,
    royalty: Cell<RoyaltyParams>,
}

struct ItemStorageNotInitialized {
    itemIndex: uint256,
    collectionAddress: address,
}

struct ItemStorageInitialized {
    itemIndex: uint256,
    collectionAddress: address,
    ownerAddress: address,
    content: cell,
}

struct ItemStorageLoader {
    itemIndex: uint256,
    collectionAddress: address,
    private rest: RemainingBitsAndRefs,
}

fun ItemStorageInitialized.startLoading(): ItemStorageLoader {
    return ItemStorageLoader.fromCell(contract.getData());
}

fun ItemStorageLoader.isNotInitialized(self): bool {
    return self.rest.isEmpty();
}

fun ItemStorageLoader.endLoading(mutate self): ItemStorageInitialized {
    return {
        itemIndex: self.itemIndex,
        collectionAddress: self.collectionAddress,
        ownerAddress: self.rest.loadAny(),
        content: self.rest.loadAny(),
    };
}

struct CollectionData {
    nextItemIndex: int,
    collectionContent: cell,
    ownerAddress: address?,
}

struct NftData {
    initialized: int,
    itemIndex: int,
    collectionAddress: address,
    ownerAddress: address?,
    content: cell?,
}
