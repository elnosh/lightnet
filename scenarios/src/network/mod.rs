use std::collections::HashMap;
use std::net::SocketAddr;
use std::str::FromStr;

use bitcoin::secp256k1::{PublicKey, SecretKey};
use serde::Deserialize;

use crate::nodes::{ldk::LdkNode, lnd::LndNode, LightningNode};
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
    // bitcoind
    #[serde(default)]
    rpc_url: String,

    // lnd
    #[serde(default)]
    grpc_url: String,
    #[serde(default)]
    macaroon_path: String,
    #[serde(default)]
    tls_cert_path: String,

    // ldk
    #[serde(default)]
    ldk_rest_url: String,

    // all LN nodes
    #[serde(default)]
    pubkey: Option<String>,
    /// Reachable from the host machine (host-mapped port).
    #[serde(default)]
    p2p_external: Option<String>,
    /// Reachable from other containers in the same Docker network.
    #[serde(default)]
    p2p_internal: Option<String>,
}

#[derive(Debug)]
pub enum Error {
    Io(std::io::Error),
    Parse(serde_json::Error),
    NotFound(String),
    InvalidNode(String),
    Node(crate::nodes::Error),
    Rpc(String),
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Error::Io(e) => write!(f, "io error: {e}"),
            Error::Parse(e) => write!(f, "parse error: {e}"),
            Error::NotFound(s) => write!(f, "not found: {s}"),
            Error::InvalidNode(s) => write!(f, "invalid node: {s}"),
            Error::Node(e) => write!(f, "node error: {e}"),
            Error::Rpc(s) => write!(f, "rpc error: {s}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Io(e) => Some(e),
            Error::Parse(e) => Some(e),
            Error::Node(e) => Some(e),
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

impl From<crate::nodes::Error> for Error {
    fn from(e: crate::nodes::Error) -> Self {
        Error::Node(e)
    }
}

/// A loaded Lightning Network from `~/.lightnet/state.json`.
pub struct Network {
    name: String,
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
        Ok(Network {
            name: name.to_string(),
            nodes: network.nodes,
        })
    }

    /// Look up a Lightning node by name for P2P wire-message connections.
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

        let pubkey_hex =
            state.connection.pubkey.as_deref().ok_or_else(|| {
                Error::InvalidNode(format!("'{name}' has no pubkey in state.json"))
            })?;
        let pubkey = PublicKey::from_str(pubkey_hex)
            .map_err(|e| Error::InvalidNode(format!("invalid pubkey for '{name}': {e}")))?;

        let addr_str =
            state.connection.p2p_external.as_deref().ok_or_else(|| {
                Error::InvalidNode(format!("'{name}' has no p2p_external address"))
            })?;
        let addr: SocketAddr = addr_str
            .parse()
            .map_err(|e| Error::InvalidNode(format!("invalid address for '{name}': {e}")))?;

        let p2p_internal = state.connection.p2p_internal.clone().unwrap_or_default();

        Ok(LnNode {
            name: name.to_string(),
            pubkey,
            addr,
            p2p_internal,
        })
    }

    /// Return a typed [`LndNode`] client for the named node.
    ///
    /// Use this instead of [`node_client`] when you need LND-specific APIs
    /// such as HODL invoices.
    pub async fn lnd_node(&self, name: &str) -> Result<LndNode, Error> {
        let state = self
            .nodes
            .get(name)
            .ok_or_else(|| Error::NotFound(format!("node '{name}' not found")))?;

        if state.kind != "lnd" {
            return Err(Error::InvalidNode(format!("'{name}' is not an LND node")));
        }

        LndNode::connect(
            &state.connection.grpc_url,
            &state.connection.tls_cert_path,
            &state.connection.macaroon_path,
        )
        .await
        .map_err(Error::Node)
    }

    /// Return an RPC client for the named Lightning node.
    ///
    /// Supports LND (`kind = "lnd"`) and LDK (`kind = "ldk"`).
    /// Returns `Error::InvalidNode` for bitcoind or CLN nodes.
    pub async fn node_client(&self, name: &str) -> Result<Box<dyn LightningNode>, Error> {
        let state = self
            .nodes
            .get(name)
            .ok_or_else(|| Error::NotFound(format!("node '{name}' not found")))?;

        match state.kind.as_str() {
            "lnd" => {
                let node = LndNode::connect(
                    &state.connection.grpc_url,
                    &state.connection.tls_cert_path,
                    &state.connection.macaroon_path,
                )
                .await?;
                Ok(Box::new(node))
            }
            "ldk" => {
                let home =
                    std::env::var("HOME").map_err(|_| Error::NotFound("HOME not set".into()))?;
                let api_key_path = format!(
                    "{home}/.lightnet/networks/{}/{name}/data/regtest/api_key",
                    self.name
                );
                let tls_cert_path = format!(
                    "{home}/.lightnet/networks/{}/{name}/data/tls.crt",
                    self.name
                );
                let node = LdkNode::connect(
                    &state.connection.ldk_rest_url,
                    &api_key_path,
                    &tls_cert_path,
                )
                .await?;
                Ok(Box::new(node))
            }
            other => Err(Error::InvalidNode(format!(
                "'{name}' has unsupported kind '{other}' for node_client (only lnd/ldk supported)"
            ))),
        }
    }

    /// Fund an address from the bitcoind test wallet and mine 10 blocks.
    ///
    /// Equivalent to `lightnet fund <network> <address> <amount>`.
    pub async fn fund(
        &self,
        bitcoind_node: &str,
        address: &str,
        amount_btc: f64,
    ) -> Result<(), Error> {
        let state = self
            .nodes
            .get(bitcoind_node)
            .ok_or_else(|| Error::NotFound(format!("node '{bitcoind_node}' not found")))?;

        if state.kind != "bitcoind" {
            return Err(Error::InvalidNode(format!(
                "'{bitcoind_node}' is not a bitcoind node"
            )));
        }

        let (base_url, username, password) = parse_rpc_url(&state.connection.rpc_url)?;
        let client = reqwest::Client::new();
        let wallet_url = format!("{base_url}/wallet/test");

        // Send funds to the target address.
        let send_resp: serde_json::Value = client
            .post(&wallet_url)
            .basic_auth(&username, Some(&password))
            .json(&serde_json::json!({
                "jsonrpc": "1.0",
                "method": "sendtoaddress",
                "params": [address, amount_btc]
            }))
            .send()
            .await
            .map_err(|e| Error::Rpc(format!("sendtoaddress request: {e}")))?
            .json()
            .await
            .map_err(|e| Error::Rpc(format!("sendtoaddress parse: {e}")))?;

        if !send_resp["error"].is_null() {
            return Err(Error::Rpc(format!(
                "sendtoaddress error: {}",
                send_resp["error"]
            )));
        }

        self.mine_blocks(bitcoind_node, 10).await
    }

    /// Mine `n` blocks on the named bitcoind node.
    ///
    /// Internally calls `getnewaddress` on the `"test"` wallet then
    /// `generatetoaddress`, so the test wallet must exist (it is created
    /// automatically by `lightnet start`).
    pub async fn mine_blocks(&self, bitcoind_node: &str, n: u32) -> Result<(), Error> {
        let state = self
            .nodes
            .get(bitcoind_node)
            .ok_or_else(|| Error::NotFound(format!("node '{bitcoind_node}' not found")))?;

        if state.kind != "bitcoind" {
            return Err(Error::InvalidNode(format!(
                "'{bitcoind_node}' is not a bitcoind node"
            )));
        }

        let rpc_url = &state.connection.rpc_url;
        let (base_url, username, password) = parse_rpc_url(rpc_url)?;

        let client = reqwest::Client::new();

        // Get a fresh address from the test wallet.
        let wallet_url = format!("{base_url}/wallet/test");
        let addr_resp: serde_json::Value = client
            .post(&wallet_url)
            .basic_auth(&username, Some(&password))
            .json(&serde_json::json!({
                "jsonrpc": "1.0",
                "method": "getnewaddress",
                "params": ["", "bech32"]
            }))
            .send()
            .await
            .map_err(|e| Error::Rpc(format!("getnewaddress request: {e}")))?
            .json()
            .await
            .map_err(|e| Error::Rpc(format!("getnewaddress parse: {e}")))?;

        let address = addr_resp["result"]
            .as_str()
            .ok_or_else(|| Error::Rpc(format!("getnewaddress returned no address: {addr_resp}")))?
            .to_string();

        // Mine the blocks.
        let gen_resp: serde_json::Value = client
            .post(&base_url)
            .basic_auth(&username, Some(&password))
            .json(&serde_json::json!({
                "jsonrpc": "1.0",
                "method": "generatetoaddress",
                "params": [n, address]
            }))
            .send()
            .await
            .map_err(|e| Error::Rpc(format!("generatetoaddress request: {e}")))?
            .json()
            .await
            .map_err(|e| Error::Rpc(format!("generatetoaddress parse: {e}")))?;

        if !gen_resp["error"].is_null() {
            return Err(Error::Rpc(format!(
                "generatetoaddress error: {}",
                gen_resp["error"]
            )));
        }

        Ok(())
    }
}

/// A Lightning node that can be connected to via BOLT #8 P2P.
pub struct LnNode {
    pub name: String,
    pub pubkey: PublicKey,
    /// Host-mapped address — use this for connections from the scenario runner.
    pub addr: SocketAddr,
    /// Docker-internal address (`container-name:port`) — use this when another
    /// container (e.g. an LND node) needs to reach this node.
    pub p2p_internal: String,
}

impl LnNode {
    /// Open an encrypted, authenticated connection to this node.
    pub async fn connect(&self, our_key: &SecretKey) -> Result<Peer, peer::Error> {
        Peer::connect(self.addr, our_key, self.pubkey).await
    }
}

/// Parse `http://user:pass@host:port` → `("http://host:port", "user", "pass")`.
fn parse_rpc_url(url: &str) -> Result<(String, String, String), Error> {
    // Split off the scheme.
    let (scheme, rest) = url
        .split_once("://")
        .ok_or_else(|| Error::Rpc(format!("invalid rpc_url '{url}': no scheme")))?;

    // Split user:pass@host:port
    let (userinfo, hostport) = rest
        .split_once('@')
        .ok_or_else(|| Error::Rpc(format!("invalid rpc_url '{url}': no '@'")))?;

    let (username, password) = userinfo
        .split_once(':')
        .ok_or_else(|| Error::Rpc(format!("invalid rpc_url '{url}': no ':' in userinfo")))?;

    let base = format!("{scheme}://{hostport}");
    Ok((base, username.to_string(), password.to_string()))
}
