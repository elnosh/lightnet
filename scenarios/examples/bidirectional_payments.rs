/// bidirectional_payments — send N payments in both directions between two nodes.
///
/// Usage:
///   cargo run --example bidirectional_payments <network> <node_a> <node_b> <num_payments>
///
/// Example:
///   cargo run --example bidirectional_payments mynetwork alice bob 100
///
/// Each iteration creates an invoice on both sides and pays them, so
/// `num_payments` total payments flow in each direction.
use scenarios::{network::Network, nodes::PaymentStatus};

const AMOUNT_MSAT: u64 = 25_000_000; // 15k sats

#[tokio::main]
async fn main() {
    let mut args = std::env::args().skip(1);
    let network_name = args
        .next()
        .expect("usage: bidirectional_payments <network> <node_a> <node_b> <num_payments>");
    let node_a_name = args.next().expect("missing <node_a>");
    let node_b_name = args.next().expect("missing <node_b>");
    let num_payments: usize = args
        .next()
        .expect("missing <num_payments>")
        .parse()
        .expect("<num_payments> must be a number");

    let network = Network::load(&network_name).unwrap_or_else(|e| {
        eprintln!("error loading network '{network_name}': {e}");
        std::process::exit(1);
    });

    println!("Connecting to nodes...");
    let node_a = network.node_client(&node_a_name).await.unwrap_or_else(|e| {
        eprintln!("error connecting to '{node_a_name}': {e}");
        std::process::exit(1);
    });
    let node_b = network.node_client(&node_b_name).await.unwrap_or_else(|e| {
        eprintln!("error connecting to '{node_b_name}': {e}");
        std::process::exit(1);
    });
    println!("Connected.");

    let mut a_to_b_succeeded = 0usize;
    let mut a_to_b_failed = 0usize;
    let mut b_to_a_succeeded = 0usize;
    let mut b_to_a_failed = 0usize;

    for i in 0..num_payments {
        let round = i + 1;

        // A -> B: create invoice on B, pay from A
        let invoice_b = node_b
            .create_invoice(AMOUNT_MSAT, &format!("a-to-b {round}"))
            .await
            .unwrap_or_else(|e| {
                eprintln!("[{round}] create_invoice on '{node_b_name}' failed: {e}");
                std::process::exit(1);
            });

        match node_a.send_payment(&invoice_b).await {
            Ok(result) => {
                println!(
                    "  [{round}] {node_a_name} -> {node_b_name}: {:?}  fee {} msat",
                    result.status, result.fee_msat
                );
                if result.status == PaymentStatus::Succeeded {
                    a_to_b_succeeded += 1;
                } else {
                    a_to_b_failed += 1;
                }
            }
            Err(e) => {
                eprintln!("  [{round}] {node_a_name} -> {node_b_name}: error: {e}");
                a_to_b_failed += 1;
            }
        }

        // B -> A: create invoice on A, pay from B
        let invoice_a = node_a
            .create_invoice(AMOUNT_MSAT, &format!("b-to-a {round}"))
            .await
            .unwrap_or_else(|e| {
                eprintln!("[{round}] create_invoice on '{node_a_name}' failed: {e}");
                std::process::exit(1);
            });

        match node_b.send_payment(&invoice_a).await {
            Ok(result) => {
                println!(
                    "  [{round}] {node_b_name} -> {node_a_name}: {:?}  fee {} msat",
                    result.status, result.fee_msat
                );
                if result.status == PaymentStatus::Succeeded {
                    b_to_a_succeeded += 1;
                } else {
                    b_to_a_failed += 1;
                }
            }
            Err(e) => {
                eprintln!("  [{round}] {node_b_name} -> {node_a_name}: error: {e}");
                b_to_a_failed += 1;
            }
        }
    }

    println!("\nResults ({num_payments} rounds):");
    println!(
        "  {node_a_name} -> {node_b_name}: {a_to_b_succeeded}/{num_payments} succeeded, {a_to_b_failed} failed"
    );
    println!(
        "  {node_b_name} -> {node_a_name}: {b_to_a_succeeded}/{num_payments} succeeded, {b_to_a_failed} failed"
    );
}
