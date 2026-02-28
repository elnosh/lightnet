use std::time::SystemTime;

use bitcoin::io;
use bitcoin::secp256k1::{PublicKey, Secp256k1, SecretKey};
use lightning::ln::peer_channel_encryptor::PeerChannelEncryptor;
use lightning::ln::{msgs, wire};
use lightning::sign::KeysManager;
use lightning::util::ser::{LengthLimitedRead, Writeable};
use rand::RngCore;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

use crate::peer::Error;

/// Uninhabited placeholder for connections that use no custom Lightning messages.
#[derive(Debug, PartialEq)]
pub enum NoCustomMessages {}

impl Writeable for NoCustomMessages {
    fn write<W: lightning::util::ser::Writer>(&self, _w: &mut W) -> Result<(), io::Error> {
        unreachable!()
    }
}

impl wire::Type for NoCustomMessages {
    fn type_id(&self) -> u16 {
        unreachable!()
    }
}

/// Thin wrapper so a byte slice satisfies `LengthLimitedRead`.
///
/// Required by `wire::read`, which needs to know how many bytes remain in order
/// to limit how many bytes each individual message decoder may consume.
struct SliceReader<'a> {
    data: &'a [u8],
    pos: usize,
}

impl<'a> SliceReader<'a> {
    fn new(data: &'a [u8]) -> Self {
        SliceReader { data, pos: 0 }
    }
}

impl<'a> io::Read for SliceReader<'a> {
    fn read(&mut self, buf: &mut [u8]) -> Result<usize, io::Error> {
        let remaining = &self.data[self.pos..];
        let len = remaining.len().min(buf.len());
        buf[..len].copy_from_slice(&remaining[..len]);
        self.pos += len;
        Ok(len)
    }
}

impl<'a> LengthLimitedRead for SliceReader<'a> {
    fn remaining_bytes(&self) -> u64 {
        (self.data.len() - self.pos) as u64
    }
}

/// Passes all unknown type IDs through as `wire::Message::Unknown`.
struct NoCustomReader;

impl wire::CustomMessageReader for NoCustomReader {
    type CustomMessage = NoCustomMessages;

    fn read<R>(
        &self,
        _type_id: u16,
        _buf: &mut R,
    ) -> Result<Option<NoCustomMessages>, msgs::DecodeError> {
        Ok(None)
    }
}

pub(crate) struct EncryptedTransport {
    stream: TcpStream,
    encryptor: PeerChannelEncryptor,
}

impl EncryptedTransport {
    pub(crate) fn new(stream: TcpStream, encryptor: PeerChannelEncryptor) -> Self {
        EncryptedTransport { stream, encryptor }
    }

    pub(crate) async fn send<T>(&mut self, msg: wire::Message<T>) -> Result<(), Error>
    where
        T: wire::Type + core::fmt::Debug,
    {
        let encrypted = self.encryptor.encrypt_message(msg);
        self.stream.write_all(&encrypted).await?;
        Ok(())
    }

    pub(crate) async fn recv(&mut self) -> Result<wire::Message<NoCustomMessages>, Error> {
        // Encrypted length header: 2-byte ciphertext + 16-byte MAC
        let mut header = [0u8; 18];
        self.stream.read_exact(&mut header).await?;

        println!("read header bytes {:?}", header);

        let msg_len = self
            .encryptor
            .decrypt_length_header(&header)
            .map_err(|e| Error::Decode(e.err))?;

        // Encrypted body: msg_len bytes + 16-byte MAC
        let mut body = vec![0u8; msg_len as usize + 16];
        self.stream.read_exact(&mut body).await?;
        println!("read body bytes {:?}", body);

        self.encryptor
            .decrypt_message(&mut body)
            .map_err(|e| Error::Decode(e.err))?;

        // Plaintext is body[..msg_len]; wire::read expects the 2-byte type prefix included
        let mut reader = SliceReader::new(&body[..msg_len as usize]);
        wire::read(&mut reader, NoCustomReader).map_err(|(e, _)| Error::Decode(format!("{e:?}")))
    }
}

pub(crate) async fn perform_outbound(
    stream: &mut TcpStream,
    our_key: &SecretKey,
    their_pubkey: PublicKey,
) -> Result<(PeerChannelEncryptor, PublicKey), Error> {
    let secp = Secp256k1::new();

    let now = SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .unwrap_or_default();
    let keys_manager = KeysManager::new(&our_key.secret_bytes(), now.as_secs(), now.subsec_nanos(), true);

    let mut key_bytes = [0u8; 32];
    rand::rngs::OsRng.fill_bytes(&mut key_bytes);
    let ephemeral = SecretKey::from_slice(&key_bytes)
        .map_err(|e| Error::Handshake(e.to_string()))?;

    let mut encryptor = PeerChannelEncryptor::new_outbound(their_pubkey, ephemeral);

    // Act 1: initiator → responder (50 bytes)
    let act_one = encryptor.get_act_one(&secp);
    stream.write_all(&act_one).await?;

    // Act 2: responder → initiator (50 bytes)
    let mut act_two = [0u8; 50];
    stream.read_exact(&mut act_two).await?;

    // Act 3: initiator → responder (66 bytes)
    let (act_three, remote_pubkey) = encryptor
        .process_act_two(&act_two, &keys_manager)
        .map_err(|e| Error::Handshake(e.err))?;
    stream.write_all(&act_three).await?;

    Ok((encryptor, remote_pubkey))
}
