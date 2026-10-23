import "types"

struct MintAuthorization {
    subwalletId: uint32,
    validSince: uint32,
    validTill: uint32,
    itemIndex: uint256,
    ownerAddress: address,
    content: cell,
}

struct (0x4637289b) MintNft {
    signature: bits512,
    authorization: Cell<MintAuthorization>,
}

struct InitializeNft {
    ownerAddress: address,
    content: cell,
}

struct (0x5fcc3d14) TransferNft {
    queryId: uint64,
    newOwner: address,
    responseDestination: address,
    customPayload: Cell<RemainingBitsAndRefs>?,
    forwardAmount: coins,
    forwardPayload: RemainingBitsAndRefs,
}

struct (0x2fcb26a2) StaticDataRequest {
    queryId: uint64,
}

struct (0x8b771735) StaticDataReport {
    queryId: uint64,
    itemIndex: uint256,
    collectionAddress: address,
}

struct (0b0) PayloadInline {
    value: RemainingBitsAndRefs,
}

struct (0b1) PayloadInRef {
    value: Cell<RemainingBitsAndRefs>,
}

struct (0x05138d91) OwnershipAssigned {
    queryId: uint64,
    previousOwner: address,
    @abi.clientType(PayloadInline | PayloadInRef)
    forwardPayload: RemainingBitsAndRefs,
}

struct (0xd53276db) Excesses {
    queryId: uint64,
}

struct (0x693d3950) RoyaltyParamsRequest {
    queryId: uint64,
}

struct (0xa8cb00ad) RoyaltyParamsReport {
    queryId: uint64,
    numerator: uint16,
    denominator: uint16,
    destination: address,
}

type CollectionMessage = MintNft | RoyaltyParamsRequest
type ItemMessage = TransferNft | StaticDataRequest
