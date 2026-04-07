/// pay_invoices — create N regular invoices on one node, pay them from
/// another, and report results.
///
/// Usage:
///   cargo run --example pay_invoices <network> <receiver> <sender>
///
/// Example:
///   cargo run --example pay_invoices mynetwork alice bob
///
/// The network must be running (`lightnet start <network>`).
/// Both nodes can be LND or LDK. A routable channel path must already exist
/// between sender and receiver.
use scenarios::{network::Network, nodes::PaymentStatus};

const NUM_INVOICES: usize = 10;
const AMOUNT_MSAT: u64 = 4_000_000;

#[tokio::main]
async fn main() {
    let mut args = std::env::args().skip(1);
    let network_name = args
        .next()
        .expect("usage: pay_invoices <network> <receiver> <sender>");
    let receiver_name = args.next().expect("missing <receiver>");
    let sender_name = args.next().expect("missing <sender>");

    let network = Network::load(&network_name).unwrap_or_else(|e| {
        eprintln!("error loading network '{network_name}': {e}");
        std::process::exit(1);
    });

    println!("Connecting to nodes...");
    let receiver = network
        .node_client(&receiver_name)
        .await
        .unwrap_or_else(|e| {
            eprintln!("error connecting to '{receiver_name}': {e}");
            std::process::exit(1);
        });
    let sender = network.node_client(&sender_name).await.unwrap_or_else(|e| {
        eprintln!("error connecting to '{sender_name}': {e}");
        std::process::exit(1);
    });
    println!("Connected.");

    // Create a BOLT11 invoice on the receiver for each payment.
    println!("\nCreating {NUM_INVOICES} invoices on '{receiver_name}'...");
    let mut invoices: Vec<String> = Vec::with_capacity(NUM_INVOICES);

    for i in 0..NUM_INVOICES {
        let bolt11 = receiver
            .create_invoice(AMOUNT_MSAT, &format!("invoice {}", i + 1))
            .await
            .unwrap_or_else(|e| {
                eprintln!("create_invoice #{} failed: {e}", i + 1);
                std::process::exit(1);
            });

        println!("  [{}] {}...", i + 1, &bolt11[..60.min(bolt11.len())]);
        invoices.push(bolt11);
    }

    // Pay each invoice. send_payment blocks until the payment reaches a
    // terminal state (succeeded or failed), so we pay them one at a time.
    println!("\nPaying {NUM_INVOICES} invoices from '{sender_name}'...");
    let mut succeeded = 0usize;
    let mut failed = 0usize;

    for (i, bolt11) in invoices.iter().enumerate() {
        match sender.send_payment(bolt11).await {
            Ok(result) => {
                println!(
                    "  [{}] {:?}  {} msat  fee {} msat",
                    i + 1,
                    result.status,
                    result.amount_msat,
                    result.fee_msat
                );
                if result.status == PaymentStatus::Succeeded {
                    succeeded += 1;
                } else {
                    failed += 1;
                }
            }
            Err(e) => {
                eprintln!("  [{}] payment error: {e}", i + 1);
                failed += 1;
            }
        }
    }

    println!("\nDone: {succeeded}/{NUM_INVOICES} succeeded, {failed} failed.");
}
