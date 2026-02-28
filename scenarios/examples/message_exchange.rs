/// message_exchange — connect to a Lightning node and exchange wire messages.
///
/// Usage: cargo run --example message_exchange <network> <node>
///
/// The network must already be running (`lightnet start <network>`).
/// A random ephemeral key pair is generated for each run.
use bitcoin::secp256k1::{PublicKey, Secp256k1, SecretKey};
use lightning::ln::{msgs::Ping, wire};
use rand::RngCore;
use scenarios::{network::Network, peer::NoCustomMessages};

#[tokio::main]
async fn main() {
    let mut args = std::env::args().skip(1);
    let network_name = args
        .next()
        .expect("usage: message_exchange <network> <node>");
    let node_name = args
        .next()
        .expect("usage: message_exchange <network> <node>");

    let mut key_bytes = [0u8; 32];
    rand::rngs::OsRng.fill_bytes(&mut key_bytes);
    let our_key = SecretKey::from_slice(&key_bytes).expect("valid 32-byte key");
    let secp = Secp256k1::new();
    let our_pubkey = PublicKey::from_secret_key(&secp, &our_key);
    println!("our ephemeral pubkey: {our_pubkey}");

    // Load network state and resolve the target node.
    let network = Network::load(&network_name).unwrap_or_else(|e| {
        eprintln!("error loading network '{network_name}': {e}");
        std::process::exit(1);
    });

    let node = network.node(&node_name).unwrap_or_else(|e| {
        eprintln!("error looking up node '{node_name}': {e}");
        std::process::exit(1);
    });

    println!(
        "connecting to {} at {} (pubkey: {})",
        node.name, node.addr, node.pubkey
    );

    // Connect: TCP + BOLT #8 handshake + Init exchange.
    let mut peer = node.connect(&our_key).await.unwrap_or_else(|e| {
        eprintln!("connection failed: {e}");
        std::process::exit(1);
    });

    println!("connected — peer node id: {}", peer.their_node_id());

    // Send a Ping and wait for the Pong.
    peer.send(wire::Message::<NoCustomMessages>::Ping(Ping {
        ponglen: 4,
        byteslen: 0,
    }))
    .await
    .expect("send ping");

    println!("sent ping");

    match peer.recv().await.expect("recv") {
        wire::Message::Pong(p) => println!("got Pong (byteslen={})", p.byteslen),
        other => println!("got unexpected message: {other:?}"),
    }
}
