import "@acton/build"
import "@acton/crypto"
import "@acton/emulation/network"
import "@acton/emulation/testing"
import "@acton/testing/expect"

import "@contracts/messages"
import "@contracts/types"
import "@wrappers/CustomFragmentCollection.gen"
import "@wrappers/CustomFragmentItem.gen"

fun setupCollection(): [CustomFragmentCollection, Treasury, KeyPair] {
    testing.setNow(1_800_000_000);
    val deployer = testing.treasury("customfragment deployer");
    val keyPair = crypto.seedToKeyPair(777);
    val collection = CustomFragmentCollection.fromStorage({
        mintedCount: 0,
        subwalletId: 0x4752414d,
        publicKey: keyPair.publicKey,
        collectionContent: createEmptyCell(),
        itemCode: build("CustomFragmentItem"),
        royalty: RoyaltyParams {
            numerator: 0,
            denominator: 1000,
            destination: deployer.address,
        }.toCell(),
    });
    collection.deploy(deployer.address, { value: ton("0.1") });
    return [collection, deployer, keyPair];
}

get fun `test customfragment: signed gift mint deploys a standard nft`() {
    val [collection, _, keyPair] = setupCollection();
    val owner = testing.treasury("gift owner");
    val authorization: MintAuthorization = {
        subwalletId: 0x4752414d,
        validSince: 1_799_999_990,
        validTill: 1_800_000_300,
        itemIndex: 42,
        ownerAddress: owner.address,
        content: beginCell().storeUint(1, 8).storeString("https://grams.example/gift/42.json").endCell(),
    };
    val signature = crypto.sign(keyPair.privateKey, authorization.toCell()) as bits512;

    val result = collection.sendMintNft(
        owner.address,
        signature,
        authorization,
        { value: ton("0.08"), bounce: true },
    );
    expect(result).toHaveSuccessfulTx<MintNft>({ from: owner.address, to: collection.address });

    val itemAddress = collection.getNftAddressByIndex(42);
    expect(result).toHaveSuccessfulDeploy({ from: collection.address, to: itemAddress });
    val data = CustomFragmentItem.fromAddress(itemAddress).getNftData();
    expect(data.initialized).toEqual(-1);
    expect(data.itemIndex).toEqual(42);
    expect(data.ownerAddress!).toEqual(owner.address);
    expect(data.collectionAddress).toEqual(collection.address);
    val fullContent = collection.getNftContent(data.itemIndex, data.content!);
    expect(fullContent.hash()).toEqual(data.content!.hash());
}

get fun `test customfragment: authorization is bound to its wallet`() {
    val [collection, _, keyPair] = setupCollection();
    val owner = testing.treasury("authorized owner");
    val attacker = testing.treasury("other wallet");
    val authorization: MintAuthorization = {
        subwalletId: 0x4752414d,
        validSince: 1_799_999_990,
        validTill: 1_800_000_300,
        itemIndex: 77,
        ownerAddress: owner.address,
        content: createEmptyCell(),
    };
    val signature = crypto.sign(keyPair.privateKey, authorization.toCell()) as bits512;
    val result = collection.sendMintNft(
        attacker.address,
        signature,
        authorization,
        { value: ton("0.08"), bounce: true },
    );
    expect(result).toHaveFailedTx<MintNft>({
        from: attacker.address,
        to: collection.address,
        exitCode: 401,
    });
}

get fun `test customfragment: TEP-62 transfer notifies the new owner and returns excesses`() {
    val [collection, _, keyPair] = setupCollection();
    val owner = testing.treasury("transfer owner");
    val newOwner = testing.treasury("transfer recipient");
    val authorization: MintAuthorization = {
        subwalletId: 0x4752414d,
        validSince: 1_799_999_990,
        validTill: 1_800_000_300,
        itemIndex: 88,
        ownerAddress: owner.address,
        content: createEmptyCell(),
    };
    val signature = crypto.sign(keyPair.privateKey, authorization.toCell()) as bits512;
    val mint = collection.sendMintNft(owner.address, signature, authorization, {
        value: ton("0.08"),
        bounce: true,
    });
    val item = CustomFragmentItem.fromAddress(collection.getNftAddressByIndex(88));
    expect(mint).toHaveSuccessfulDeploy({ from: collection.address, to: item.address });

    // Exact transfer body used by Getgems v4: response goes to the new owner,
    // one nanoton forces an ownership_assigned notification and the final zero
    // bit is an inline empty forward_payload.
    val transferBody = beginCell()
        .storeUint(0x5fcc3d14, 32)
        .storeUint(88, 64)
        .storeAddress(newOwner.address)
        .storeAddress(newOwner.address)
        .storeUint(0, 1)
        .storeCoins(1)
        .storeUint(0, 1)
        .endCell();
    val transferred = item.sendAny(owner.address, transferBody, { value: ton("0.1"), bounce: true });
    expect(transferred).toHaveSuccessfulTx<TransferNft>({ from: owner.address, to: item.address });
    // A one-nanoton notification to a plain address can have its recipient
    // compute phase skipped; what TEP-62 requires here is the emitted message.
    expect(transferred).toHaveTx<OwnershipAssigned>({ from: item.address, to: newOwner.address });
    expect(transferred).toHaveSuccessfulTx<Excesses>({ from: item.address, to: newOwner.address });
    expect(item.getNftData().ownerAddress!).toEqual(newOwner.address);
}

get fun `test customfragment: standard TEP-62 static data request replies to sender`() {
    val [collection, _, keyPair] = setupCollection();
    val owner = testing.treasury("static data owner");
    val requester = testing.treasury("static data requester");
    val authorization: MintAuthorization = {
        subwalletId: 0x4752414d,
        validSince: 1_799_999_990,
        validTill: 1_800_000_300,
        itemIndex: 99,
        ownerAddress: owner.address,
        content: createEmptyCell(),
    };
    val signature = crypto.sign(keyPair.privateKey, authorization.toCell()) as bits512;
    val mint = collection.sendMintNft(owner.address, signature, authorization, {
        value: ton("0.08"),
        bounce: true,
    });
    val item = CustomFragmentItem.fromAddress(collection.getNftAddressByIndex(99));
    expect(mint).toHaveSuccessfulDeploy({ from: collection.address, to: item.address });

    val requestBody = beginCell()
        .storeUint(0x2fcb26a2, 32)
        .storeUint(123, 64)
        .endCell();
    val result = item.sendAny(requester.address, requestBody, { value: ton("0.05"), bounce: true });
    expect(result).toHaveSuccessfulTx<StaticDataRequest>({ from: requester.address, to: item.address });
    expect(result).toHaveSuccessfulTx<StaticDataReport>({ from: item.address, to: requester.address });
}
