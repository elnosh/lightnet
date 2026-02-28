use std::collections::HashMap;
use std::net::SocketAddr;
use std::str::FromStr;

use bitcoin::secp256k1::{PublicKey, SecretKey};
use serde::Deserialize;

use crate::peer::{self, Peer};

// ---------------------------------------------------------------------------
// Private state types — mirrors the Go state.json schema
// ---------------------------------------------------------------------------

#[derive(Deserialize)]
struct GlobalState {
    networks: HashMap<String, RunningNetwork>,
}

#[derive(Deserialize)]
struct RunningNetwork {
    nodes: HashMap<String, NodeState>,
}

#[derive(Deserialize)]
struct NodeState {
    kind: String,
    connection: ConnectionInfo,
}

#[derive(Deserialize)]
struct ConnectionInfo {
    pubkey: Option<String>,
    p2p_external: Option<String>,
}

// ---------------------------------------------------------------------------
// Public error type
// ---------------------------------------------------------------------------

#[derive(Debug)]
pub enum Error {
    Io(std::io::Error),
    Parse(serde_json::Error),
    NotFound(String),
    InvalidNode(String),
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Error::Io(e) => write!(f, "io error: {e}"),
            Error::Parse(e) => write!(f, "parse error: {e}"),
            Error::NotFound(s) => write!(f, "not found: {s}"),
            Error::InvalidNode(s) => write!(f, "invalid node: {s}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Io(e) => Some(e),
            Error::Parse(e) => Some(e),
            _ => None,
        }
    }
}

impl From<std::io::Error> for Error {
    fn from(e: std::io::Error) -> Self {
        Error::Io(e)
    }
}

impl From<serde_json::Error> for Error {
    fn from(e: serde_json::Error) -> Self {
        Error::Parse(e)
    }
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/// A loaded Lightning Network from `~/.lightnet/state.json`.
pub struct Network {
    nodes: HashMap<String, NodeState>,
}

impl Network {
    /// Load a named network from `~/.lightnet/state.json`.
    pub fn load(name: &str) -> Result<Self, Error> {
        let home = std::env::var("HOME")
            .map_err(|_| Error::NotFound("HOME environment variable not set".into()))?;
        let path = format!("{home}/.lightnet/state.json");
        let data = std::fs::read_to_string(&path)?;
        let mut state: GlobalState = serde_json::from_str(&data)?;
        let network = state
            .networks
            .remove(name)
            .ok_or_else(|| Error::NotFound(format!("network '{name}' not in state.json")))?;
        Ok(Network { nodes: network.nodes })
    }

    /// Look up a Lightning node by name.
    ///
    /// Returns an error if the name is not found or if it refers to a bitcoind node.
    pub fn node(&self, name: &str) -> Result<LnNode, Error> {
        let state = self
            .nodes
            .get(name)
            .ok_or_else(|| Error::NotFound(format!("node '{name}' not found")))?;

        if state.kind == "bitcoind" {
            return Err(Error::InvalidNode(format!(
                "'{name}' is a bitcoind node, not a Lightning node"
            )));
        }

        let pubkey_hex = state
            .connection
            .pubkey
            .as_deref()
            .ok_or_else(|| Error::InvalidNode(format!("'{name}' has no pubkey in state.json")))?;
        let pubkey = PublicKey::from_str(pubkey_hex)
            .map_err(|e| Error::InvalidNode(format!("invalid pubkey for '{name}': {e}")))?;

        let addr_str = state
            .connection
            .p2p_external
            .as_deref()
            .ok_or_else(|| Error::InvalidNode(format!("'{name}' has no p2p_external address")))?;
        let addr: SocketAddr = addr_str
            .parse()
            .map_err(|e| Error::InvalidNode(format!("invalid address for '{name}': {e}")))?;

        Ok(LnNode { name: name.to_string(), pubkey, addr })
    }
}

/// A Lightning node that can be connected to.
pub struct LnNode {
    pub name: String,
    pub pubkey: PublicKey,
    pub addr: SocketAddr,
}

impl LnNode {
    /// Open an encrypted, authenticated connection to this node.
    pub async fn connect(&self, our_key: &SecretKey) -> Result<Peer, peer::Error> {
        Peer::connect(self.addr, our_key, self.pubkey).await
    }
}
