pub mod ldk;
pub mod lnd;

/// An open channel between two peers.
#[derive(Debug, Clone)]
pub struct Channel {
    /// Opaque identifier: `"txid:vout"` for LND, `user_channel_id` hex for LDK.
    pub id: String,
    /// Hex-encoded pubkey of the remote peer.
    pub peer_pubkey: String,
    pub local_balance_sat: u64,
    pub capacity_sat: u64,
    pub active: bool,
}

#[derive(Debug, Clone)]
pub struct PaymentResult {
    /// Hex-encoded payment hash.
    pub payment_hash: String,
    pub status: PaymentStatus,
    pub amount_msat: u64,
    pub fee_msat: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PaymentStatus {
    Succeeded,
    Failed,
    InFlight,
}

#[derive(Debug)]
pub enum Error {
    Connect(String),
    Rpc(String),
    InvalidArgument(String),
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Error::Connect(s) => write!(f, "connect error: {s}"),
            Error::Rpc(s) => write!(f, "rpc error: {s}"),
            Error::InvalidArgument(s) => write!(f, "invalid argument: {s}"),
        }
    }
}

impl std::error::Error for Error {}

/// High-level programmatic interface to a Lightning node.
///
/// Implemented by [`lnd::LndNode`] and [`ldk::LdkNode`].
/// Use [`crate::network::Network::node_client`] to get a `Box<dyn LightningNode>`
/// when you don't need implementation-specific APIs.
#[async_trait::async_trait]
pub trait LightningNode {
    /// Connect to the peer (if needed) and open a channel.
    ///
    /// Returns a [`Channel`] representing the newly funded channel.
    /// The channel will not be active until the funding transaction is
    /// confirmed — call `mine_blocks` to advance the chain.
    async fn open_channel(
        &self,
        peer_pubkey: &str,
        peer_addr: &str,
        local_sat: u64,
        push_msat: u64,
    ) -> Result<Channel, Error>;

    /// Cooperatively close a channel.
    async fn close_channel(&self, channel: &Channel) -> Result<(), Error>;

    /// Unilaterally force-close a channel.
    ///
    /// Returns once the force-close transaction is broadcast. Funds are not
    /// immediately spendable — the to-local output has a CSV delay.
    async fn force_close_channel(&self, channel: &Channel) -> Result<(), Error>;

    /// Return all channels (active and inactive).
    async fn list_channels(&self) -> Result<Vec<Channel>, Error>;

    /// Create a BOLT11 receive invoice for the given amount.
    async fn create_invoice(&self, amount_msat: u64, description: &str) -> Result<String, Error>;

    /// Pay a BOLT11 invoice.
    ///
    /// Blocks until the payment reaches a terminal state (succeeded or failed).
    async fn send_payment(&self, bolt11: &str) -> Result<PaymentResult, Error>;

    /// Return a new on-chain address for depositing funds into this node's wallet.
    async fn new_address(&self) -> Result<String, Error>;
}
