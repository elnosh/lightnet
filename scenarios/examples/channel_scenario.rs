/// channel_scenario — open, pay, and close a channel between two nodes.
///
/// Usage:
///   cargo run --example channel_scenario <network> <lnd-node> <ldk-node> <bitcoind-node>
///
/// Example:
///   cargo run --example channel_scenario mynetwork alice dave btc1
///
/// The network must be running (`lightnet start <network>`).
/// Both an LND and an LDK node must be present.
use scenarios::network::Network;

#[tokio::main]
async fn main() {
    let mut args = std::env::args().skip(1);
    let network_name = args
        .next()
        .expect("usage: channel_scenario <network> <lnd-node> <ldk-node> <bitcoind-node>");
    let lnd_name = args.next().expect("missing <lnd-node>");
    let ldk_name = args.next().expect("missing <ldk-node>");
    let btc_name = args.next().expect("missing <bitcoind-node>");

    let network = Network::load(&network_name).unwrap_or_else(|e| {
        eprintln!("error loading network '{network_name}': {e}");
        std::process::exit(1);
    });

    println!("Connecting to nodes...");

    let alice = network.node_client(&lnd_name).await.unwrap_or_else(|e| {
        eprintln!("error connecting to LND node '{lnd_name}': {e}");
        std::process::exit(1);
    });

    let dave = network.node_client(&ldk_name).await.unwrap_or_else(|e| {
        eprintln!("error connecting to LDK node '{ldk_name}': {e}");
        std::process::exit(1);
    });

    // Get Dave's P2P info from the network state (for the channel open).
    let dave_ln = network.node(&ldk_name).unwrap_or_else(|e| {
        eprintln!("error looking up LDK node '{ldk_name}': {e}");
        std::process::exit(1);
    });
    let dave_pubkey = dave_ln.pubkey.to_string();
    // Use p2p_internal: Alice's LND daemon connects from inside Docker,
    // so it must reach Dave via the Docker-internal address, not the host-mapped port.
    let dave_addr = dave_ln.p2p_internal.clone();

    // --- Step 0: Fund both nodes' on-chain wallets. ---------------------------
    // Alice needs funds to open the channel; Dave needs funds to cover the
    // Anchor channel on-chain reserve (ldk-server rejects otherwise).
    println!("\n[0/7] Funding on-chain wallets...");

    let alice_address = alice.new_address().await.unwrap_or_else(|e| {
        eprintln!("new_address failed for {lnd_name}: {e}");
        std::process::exit(1);
    });
    network.fund(&btc_name, &alice_address, 1.0).await.unwrap_or_else(|e| {
        eprintln!("fund failed for {lnd_name}: {e}");
        std::process::exit(1);
    });
    println!("  {lnd_name}: funded {alice_address} with 1 BTC.");

    let dave_address = dave.new_address().await.unwrap_or_else(|e| {
        eprintln!("new_address failed for {ldk_name}: {e}");
        std::process::exit(1);
    });
    network.fund(&btc_name, &dave_address, 0.01).await.unwrap_or_else(|e| {
        eprintln!("fund failed for {ldk_name}: {e}");
        std::process::exit(1);
    });
    println!("  {ldk_name}: funded {dave_address} with 0.01 BTC.");

    // Give wallets time to sync the confirmed UTXOs.
    tokio::time::sleep(std::time::Duration::from_secs(3)).await;

    // --- Step 1: Alice opens a 1 000 000 sat channel to Dave. ----------------
    println!("\n[1/7] Opening channel: {lnd_name} → {ldk_name} (1 000 000 sat)");
    let chan = alice
        .open_channel(&dave_pubkey, &dave_addr, 1_000_000, 0)
        .await
        .unwrap_or_else(|e| {
            eprintln!("open_channel failed: {e}");
            std::process::exit(1);
        });
    println!("  channel id: {}", chan.id);

    // --- Step 2: Mine 6 blocks to confirm the funding transaction. -----------
    println!("\n[2/7] Mining 6 blocks on '{btc_name}'...");
    network.mine_blocks(&btc_name, 6).await.unwrap_or_else(|e| {
        eprintln!("mine_blocks failed: {e}");
        std::process::exit(1);
    });
    println!("  done.");

    // Give nodes a moment to process the new block.
    tokio::time::sleep(std::time::Duration::from_secs(2)).await;

    // --- Step 3: List channels on both sides to confirm active. --------------
    println!("\n[3/7] Listing channels on {lnd_name}...");
    let alice_chans = alice.list_channels().await.unwrap_or_else(|e| {
        eprintln!("list_channels failed: {e}");
        std::process::exit(1);
    });
    for c in &alice_chans {
        println!(
            "  id={} peer={} capacity={}sat active={}",
            c.id, c.peer_pubkey, c.capacity_sat, c.active
        );
    }

    println!("\n[3/7] Listing channels on {ldk_name}...");
    let dave_chans = dave.list_channels().await.unwrap_or_else(|e| {
        eprintln!("list_channels failed: {e}");
        std::process::exit(1);
    });
    for c in &dave_chans {
        println!(
            "  id={} peer={} capacity={}sat active={}",
            c.id, c.peer_pubkey, c.capacity_sat, c.active
        );
    }

    // --- Step 4: Dave creates a 50 000 sat invoice. --------------------------
    println!("\n[4/7] Dave creates a 50 000 sat invoice...");
    let invoice = dave
        .create_invoice(50_000_000, "channel scenario test payment")
        .await
        .unwrap_or_else(|e| {
            eprintln!("create_invoice failed: {e}");
            std::process::exit(1);
        });
    println!("  invoice: {}", &invoice[..60.min(invoice.len())]);

    // --- Step 5: Alice pays Dave's invoice. ----------------------------------
    println!("\n[5/7] Alice pays invoice...");
    let payment = alice.send_payment(&invoice).await.unwrap_or_else(|e| {
        eprintln!("send_payment failed: {e}");
        std::process::exit(1);
    });
    println!("  status: {:?}", payment.status);
    println!("  amount: {} msat", payment.amount_msat);
    println!("  fee:    {} msat", payment.fee_msat);

    // --- Step 6: Cooperative close. ------------------------------------------
    println!("\n[6/7] Alice cooperatively closes the channel...");
    alice.close_channel(&chan).await.unwrap_or_else(|e| {
        eprintln!("close_channel failed: {e}");
        std::process::exit(1);
    });
    println!("  close initiated.");

    // --- Step 7: Mine 6 blocks to finalize the close. -----------------------
    println!("\n[7/7] Mining 6 blocks to finalize close...");
    network.mine_blocks(&btc_name, 6).await.unwrap_or_else(|e| {
        eprintln!("mine_blocks (close) failed: {e}");
        std::process::exit(1);
    });
    println!("  done.\n");

    println!("Scenario complete.");
}
