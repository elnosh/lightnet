use std::path::{Path, PathBuf};

use fedimint_tonic_lnd::{invoicesrpc, lnrpc, routerrpc, tonic};
use tokio::sync::Mutex;

use super::{Channel, Error, LightningNode, PaymentResult, PaymentStatus};

/// A live payment update stream returned by [`LndNode::send_payment_stream`].
///
/// The lock on the underlying gRPC client is released as soon as the stream is
/// returned, so multiple streams can be polled concurrently.
pub struct LndPaymentStream {
    inner: tonic::Streaming<lnrpc::Payment>,
}

impl LndPaymentStream {
    /// Wait for the payment to reach a terminal state and return the result.
    ///
    /// Returns `None` if the stream ends without a terminal update.
    pub async fn result(mut self) -> Option<Result<PaymentResult, Error>> {
        loop {
            match self.inner.message().await {
                Ok(Some(payment)) => match payment.status() {
                    lnrpc::payment::PaymentStatus::Succeeded => {
                        return Some(Ok(PaymentResult {
                            payment_hash: payment.payment_hash,
                            status: PaymentStatus::Succeeded,
                            amount_msat: payment.value_msat as u64,
                            fee_msat: payment.fee_msat as u64,
                        }));
                    }
                    lnrpc::payment::PaymentStatus::Failed => {
                        return Some(Ok(PaymentResult {
                            payment_hash: payment.payment_hash,
                            status: PaymentStatus::Failed,
                            amount_msat: payment.value_msat as u64,
                            fee_msat: payment.fee_msat as u64,
                        }));
                    }
                    _ => continue,
                },
                Ok(None) => return None,
                Err(e) => return Some(Err(Error::Rpc(e.to_string()))),
            }
        }
    }
}

/// An LND node accessed via gRPC (fedimint-tonic-lnd).
pub struct LndNode {
    client: Mutex<fedimint_tonic_lnd::Client>,
}

impl LndNode {
    /// Connect to an LND node.
    ///
    /// `grpc_url` should be `host:port` (e.g. `"localhost:10009"`); `https://`
    /// is prepended automatically.
    /// Initiate a payment and return a stream for tracking its progress.
    ///
    /// Unlike the trait's `send_payment`, this releases the client lock as
    /// soon as the payment is initiated, so multiple payments can be in-flight
    /// at the same time. Call [`LndPaymentStream::result`] to wait for the
    /// terminal outcome.
    pub async fn send_payment_stream(&self, bolt11: &str) -> Result<LndPaymentStream, Error> {
        let stream = {
            let mut client = self.client.lock().await;
            client
                .router()
                .send_payment_v2(routerrpc::SendPaymentRequest {
                    payment_request: bolt11.to_string(),
                    timeout_seconds: 60,
                    fee_limit_sat: i64::MAX,
                    ..Default::default()
                })
                .await
                .map_err(|e| Error::Rpc(e.to_string()))?
                .into_inner()
        };
        Ok(LndPaymentStream { inner: stream })
    }

    /// Create a HODL invoice tied to `payment_hash`.
    ///
    /// The invoice will remain in the `ACCEPTED` state after the sender pays it
    /// until the preimage corresponding to `payment_hash` is revealed via
    /// `settle_hodl_invoice`, or the invoice is cancelled.
    pub async fn create_hodl_invoice(
        &self,
        payment_hash: [u8; 32],
        amount_msat: u64,
        description: &str,
    ) -> Result<String, Error> {
        let mut client = self.client.lock().await;
        let response = client
            .invoices()
            .add_hold_invoice(invoicesrpc::AddHoldInvoiceRequest {
                memo: description.to_string(),
                hash: payment_hash.to_vec(),
                value_msat: amount_msat as i64,
                ..Default::default()
            })
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?
            .into_inner();

        Ok(response.payment_request)
    }

    /// Settle a HODL invoice by revealing the preimage.
    ///
    /// The preimage must hash to the payment hash used when creating the invoice.
    pub async fn settle_hodl_invoice(&self, preimage: [u8; 32]) -> Result<(), Error> {
        let mut client = self.client.lock().await;
        client
            .invoices()
            .settle_invoice(invoicesrpc::SettleInvoiceMsg {
                preimage: preimage.to_vec(),
            })
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?;

        Ok(())
    }

    pub async fn connect(
        grpc_url: &str,
        tls_cert_path: impl AsRef<Path> + Into<PathBuf> + std::fmt::Debug,
        macaroon_path: impl AsRef<Path> + Into<PathBuf> + std::fmt::Debug,
    ) -> Result<Self, Error> {
        let address = format!("https://{grpc_url}");
        let client = fedimint_tonic_lnd::connect(address, tls_cert_path, macaroon_path)
            .await
            .map_err(|e| Error::Connect(e.to_string()))?;
        Ok(LndNode {
            client: Mutex::new(client),
        })
    }
}

#[async_trait::async_trait]
impl LightningNode for LndNode {
    async fn open_channel(
        &self,
        peer_pubkey: &str,
        peer_addr: &str,
        local_sat: u64,
        push_msat: u64,
    ) -> Result<Channel, Error> {
        let pubkey_bytes = hex::decode(peer_pubkey)
            .map_err(|e| Error::InvalidArgument(format!("invalid pubkey hex: {e}")))?;

        let mut client = self.client.lock().await;

        // Connect to peer first; ignore "already connected" errors.
        let connect_result = client
            .lightning()
            .connect_peer(lnrpc::ConnectPeerRequest {
                addr: Some(lnrpc::LightningAddress {
                    pubkey: peer_pubkey.to_string(),
                    host: peer_addr.to_string(),
                }),
                perm: false,
                timeout: 10,
            })
            .await;
        if let Err(status) = connect_result {
            if !status.message().contains("already connected") {
                return Err(Error::Rpc(status.to_string()));
            }
        }

        let push_sat = push_msat / 1000;
        let response = client
            .lightning()
            .open_channel_sync(lnrpc::OpenChannelRequest {
                node_pubkey: pubkey_bytes,
                local_funding_amount: local_sat as i64,
                push_sat: push_sat as i64,
                ..Default::default()
            })
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?
            .into_inner();

        // Build a stable channel ID from the ChannelPoint.
        let txid_hex = match response.funding_txid {
            Some(lnrpc::channel_point::FundingTxid::FundingTxidBytes(bytes)) => hex::encode(&bytes),
            Some(lnrpc::channel_point::FundingTxid::FundingTxidStr(s)) => s,
            None => return Err(Error::Rpc("no funding txid in ChannelPoint".into())),
        };
        let channel_id = format!("{txid_hex}:{}", response.output_index);

        Ok(Channel {
            id: channel_id,
            peer_pubkey: peer_pubkey.to_string(),
            local_balance_sat: local_sat,
            capacity_sat: local_sat,
            active: false,
        })
    }

    async fn close_channel(&self, channel: &Channel) -> Result<(), Error> {
        let channel_point = parse_channel_point(&channel.id)?;
        let mut client = self.client.lock().await;
        let mut stream = client
            .lightning()
            .close_channel(lnrpc::CloseChannelRequest {
                channel_point: Some(channel_point),
                force: false,
                no_wait: true,
                ..Default::default()
            })
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?
            .into_inner();

        // Drain the update stream until the channel is confirmed closed.
        loop {
            match stream
                .message()
                .await
                .map_err(|e| Error::Rpc(e.to_string()))?
            {
                Some(update) => {
                    if let Some(lnrpc::close_status_update::Update::ChanClose(_)) = update.update {
                        break;
                    }
                }
                None => break,
            }
        }
        Ok(())
    }

    async fn force_close_channel(&self, channel: &Channel) -> Result<(), Error> {
        let channel_point = parse_channel_point(&channel.id)?;
        let mut client = self.client.lock().await;
        let mut stream = client
            .lightning()
            .close_channel(lnrpc::CloseChannelRequest {
                channel_point: Some(channel_point),
                force: true,
                ..Default::default()
            })
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?
            .into_inner();

        // Wait for the force-close tx to be broadcast (PendingUpdate), then return.
        // The channel will be fully settled after the CSV delay — callers must
        // mine blocks separately.
        loop {
            match stream
                .message()
                .await
                .map_err(|e| Error::Rpc(e.to_string()))?
            {
                Some(update) => {
                    if let Some(lnrpc::close_status_update::Update::ClosePending(_)) = update.update
                    {
                        break;
                    }
                    if let Some(lnrpc::close_status_update::Update::ChanClose(_)) = update.update {
                        break;
                    }
                }
                None => break,
            }
        }
        Ok(())
    }

    async fn list_channels(&self) -> Result<Vec<Channel>, Error> {
        let mut client = self.client.lock().await;
        let response = client
            .lightning()
            .list_channels(lnrpc::ListChannelsRequest::default())
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?
            .into_inner();

        let channels = response
            .channels
            .into_iter()
            .map(|ch| Channel {
                id: ch.channel_point,
                peer_pubkey: ch.remote_pubkey,
                local_balance_sat: ch.local_balance as u64,
                capacity_sat: ch.capacity as u64,
                active: ch.active,
            })
            .collect();

        Ok(channels)
    }

    async fn create_invoice(&self, amount_msat: u64, description: &str) -> Result<String, Error> {
        let mut client = self.client.lock().await;
        let response = client
            .lightning()
            .add_invoice(lnrpc::Invoice {
                memo: description.to_string(),
                value_msat: amount_msat as i64,
                ..Default::default()
            })
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?
            .into_inner();

        Ok(response.payment_request)
    }

    async fn send_payment(&self, bolt11: &str) -> Result<PaymentResult, Error> {
        let mut client = self.client.lock().await;
        let mut stream = client
            .router()
            .send_payment_v2(routerrpc::SendPaymentRequest {
                payment_request: bolt11.to_string(),
                timeout_seconds: 60,
                fee_limit_sat: i64::MAX,
                ..Default::default()
            })
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?
            .into_inner();

        loop {
            match stream
                .message()
                .await
                .map_err(|e| Error::Rpc(e.to_string()))?
            {
                Some(payment) => {
                    let status = payment.status();
                    match status {
                        lnrpc::payment::PaymentStatus::Succeeded => {
                            return Ok(PaymentResult {
                                payment_hash: payment.payment_hash,
                                status: PaymentStatus::Succeeded,
                                amount_msat: payment.value_msat as u64,
                                fee_msat: payment.fee_msat as u64,
                            });
                        }
                        lnrpc::payment::PaymentStatus::Failed => {
                            return Ok(PaymentResult {
                                payment_hash: payment.payment_hash,
                                status: PaymentStatus::Failed,
                                amount_msat: payment.value_msat as u64,
                                fee_msat: payment.fee_msat as u64,
                            });
                        }
                        _ => continue,
                    }
                }
                None => {
                    return Err(Error::Rpc(
                        "payment stream ended without terminal status".into(),
                    ))
                }
            }
        }
    }
    async fn new_address(&self) -> Result<String, Error> {
        let mut client = self.client.lock().await;
        let response = client
            .lightning()
            .new_address(lnrpc::NewAddressRequest {
                r#type: lnrpc::AddressType::TaprootPubkey as i32,
                ..Default::default()
            })
            .await
            .map_err(|e| Error::Rpc(e.to_string()))?
            .into_inner();
        Ok(response.address)
    }
}

/// Parse a `"txid_hex:vout"` channel point string into an `lnrpc::ChannelPoint`.
fn parse_channel_point(id: &str) -> Result<lnrpc::ChannelPoint, Error> {
    let (txid_hex, vout_str) = id
        .split_once(':')
        .ok_or_else(|| Error::InvalidArgument(format!("channel id has no ':': {id}")))?;
    let output_index: u32 = vout_str
        .parse()
        .map_err(|e| Error::InvalidArgument(format!("invalid vout in channel id '{id}': {e}")))?;
    let txid_bytes = hex::decode(txid_hex)
        .map_err(|e| Error::InvalidArgument(format!("invalid txid hex '{txid_hex}': {e}")))?;

    Ok(lnrpc::ChannelPoint {
        funding_txid: Some(lnrpc::channel_point::FundingTxid::FundingTxidBytes(
            txid_bytes,
        )),
        output_index,
    })
}
