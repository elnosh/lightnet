pub(crate) mod transport;

pub use transport::NoCustomMessages;

use std::net::SocketAddr;

use bitcoin::secp256k1::{PublicKey, SecretKey};
use lightning::{
    ln::{channelmanager::provided_init_features, msgs, wire},
    util::config::UserConfig,
};
use tokio::net::TcpStream;

use transport::EncryptedTransport;

#[derive(Debug)]
pub enum Error {
    Io(std::io::Error),
    Handshake(String),
    Decode(String),
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Error::Io(e) => write!(f, "io error: {e}"),
            Error::Handshake(s) => write!(f, "handshake error: {s}"),
            Error::Decode(s) => write!(f, "decode error: {s}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        if let Error::Io(e) = self {
            Some(e)
        } else {
            None
        }
    }
}

impl From<std::io::Error> for Error {
    fn from(e: std::io::Error) -> Self {
        Error::Io(e)
    }
}

/// An encrypted, authenticated connection to a Lightning peer.
pub struct Peer {
    transport: EncryptedTransport,
    their_node_id: PublicKey,
}

impl Peer {
    /// Connect to a Lightning peer: TCP connect, BOLT #8 handshake, Init exchange.
    pub async fn connect(
        addr: SocketAddr,
        our_key: &SecretKey,
        their_pubkey: PublicKey,
    ) -> Result<Self, Error> {
        let mut stream = TcpStream::connect(addr).await?;

        let (encryptor, their_node_id) =
            transport::perform_outbound(&mut stream, our_key, their_pubkey).await?;

        let mut transport = EncryptedTransport::new(stream, encryptor);

        let conf = UserConfig::default();
        let init = msgs::Init {
            features: provided_init_features(&conf),
            networks: None,
            remote_network_address: None,
        };
        transport
            .send(wire::Message::<NoCustomMessages>::Init(init))
            .await?;

        match transport.recv().await? {
            wire::Message::Init(init) => {
                println!("got Init {:?}", init);
            }
            other => return Err(Error::Handshake(format!("expected Init, got {other:?}"))),
        }

        Ok(Peer {
            transport,
            their_node_id,
        })
    }

    pub fn their_node_id(&self) -> PublicKey {
        self.their_node_id
    }

    /// Encrypt and send a Lightning wire message to the peer.
    pub async fn send<T>(&mut self, msg: wire::Message<T>) -> Result<(), Error>
    where
        T: wire::Type + core::fmt::Debug,
    {
        self.transport.send(msg).await
    }

    /// Return the next inbound message.
    ///
    /// Peer Pings are answered automatically and not surfaced to the caller.
    /// Pongs (responses to our own Pings) are silently discarded.
    pub async fn recv(&mut self) -> Result<wire::Message<NoCustomMessages>, Error> {
        loop {
            let msg = self.transport.recv().await?;
            match msg {
                wire::Message::Ping(ping) => {
                    let pong = wire::Message::<NoCustomMessages>::Pong(msgs::Pong {
                        byteslen: ping.ponglen,
                    });
                    self.transport.send(pong).await?;
                }
                wire::Message::Pong(_) => {}
                other => return Ok(other),
            }
        }
    }
}
