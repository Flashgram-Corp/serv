import "@acton/build"
import "@acton/crypto"
import "@acton/emulation/network"
import "@acton/emulation/scripts"
import "@acton/env"
import "@acton/io"

import "@contracts/types"
import "@wrappers/CustomFragmentCollection.gen"

struct (0x01) OffchainMetadata {
    url: string,
}

/// Deploys the one CustomFragment collection used for Gramsrv gifts. The mint
/// authority seed is supplied through the process environment and is never
/// written into contract storage; only its Ed25519 public key is persisted.
///
/// Required environment:
///   CUSTOM_FRAGMENT_SIGNING_SEED=0x<32-byte hex seed>
/// Optional environment:
///   CUSTOM_FRAGMENT_METADATA_URL=https://host/custom-fragment/collection.json
fun main() {
    val deployer = scripts.wallet("customfragment-deployer");
    val authoritySeed = env<int>("CUSTOM_FRAGMENT_SIGNING_SEED");
    if (authoritySeed == null) {
        throw 400;
    }
    val metadataUrl = env<string>("CUSTOM_FRAGMENT_METADATA_URL")
        ?? "https://test-invgram.duckdns.org/custom-fragment/collection.json";
    val authority = crypto.seedToKeyPair(authoritySeed as uint256);

    val collection = CustomFragmentCollection.fromStorage({
        mintedCount: 0,
        subwalletId: 0x4752414d,
        publicKey: authority.publicKey,
        collectionContent: OffchainMetadata { url: metadataUrl }.toCell(),
        itemCode: build("CustomFragmentItem"),
        royalty: RoyaltyParams {
            numerator: 0,
            denominator: 1000,
            destination: deployer.address,
        }.toCell(),
    });

    println("Mint authority public key: 0x{:x}", authority.publicKey);
    println("CustomFragment collection address: {}", collection.address);
    // Keep the initial reserve small: mint messages pay their own execution and
    // forward the remaining value to the item. Anyone can top the collection
    // up later with an empty internal message if storage rent ever requires it.
    val result = collection.deploy(deployer.address, { value: ton("0.03"), bounce: false });
    result.waitForFirstTransaction();
    println("CustomFragment collection deployed: {}", collection.address);
}
