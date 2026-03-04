use std::path::Path;
use std::time::Duration;

use ldk_server_client::client::LdkServerClient;
use ldk_server_client::ldk_server_protos::api::{
    Bolt11ReceiveRequest, Bolt11SendRequest, CloseChannelRequest, ForceCloseChannelRequest,
    GetPaymentDetailsRequest, ListChannelsRequest, OnchainReceiveRequest, OpenChannelRequest,
};
use ldk_server_client::ldk_server_protos::types::{
    bolt11_invoice_description, payment_kind, Bolt11InvoiceDescription,
    PaymentStatus as LdkPaymentStatus,
};

use super::{Channel, Error, LightningNode, PaymentResult, PaymentStatus};

/// An LDK node accessed via the ldk-server REST API.
pub struct LdkNode {
    client: LdkServerClient,
}

impl LdkNode {
    /// Connect to an ldk-server node.
    ///
    /// `rest_url` may include the `https://` scheme prefix (it will be stripped
    /// internally, since `LdkServerClient` expects `"host:port"` only).
    ///
    /// `api_key_path` — path to the binary API key file
    /// (`<data_dir>/regtest/api_key`).  The key is hex-encoded before being
    /// passed to the client (matching `ldk-cli` behaviour).
    ///
    /// `tls_cert_path` — path to the server TLS certificate PEM
    /// (`<data_dir>/tls.crt`).
    pub async fn connect(
        rest_url: &str,
        api_key_path: impl AsRef<Path>,
        tls_cert_path: impl AsRef<Path>,
    ) -> Result<Self, Error> {
        // Strip scheme so the client receives only "host:port".
        let base_url = rest_url
            .strip_prefix("https://")
            .or_else(|| rest_url.strip_prefix("http://"))
            .unwrap_or(rest_url)
            .to_string();

        // The binary API key is hex-encoded for the client (matching ldk-cli behaviour).
        let api_key_bytes = std::fs::read(api_key_path.as_ref()).map_err(|e| {
            Error::Connect(format!(
                "reading api_key at {:?}: {e}",
                api_key_path.as_ref()
            ))
        })?;
        let api_key = hex::encode(&api_key_bytes);

        let cert_pem = std::fs::read(tls_cert_path.as_ref()).map_err(|e| {
            Error::Connect(format!(
                "reading tls cert at {:?}: {e}",
                tls_cert_path.as_ref()
            ))
        })?;

        let client = LdkServerClient::new(base_url, api_key, &cert_pem)
            .map_err(|e| Error::Connect(format!("ldk-server client init: {e}")))?;

        Ok(LdkNode { client })
    }
}

#[async_trait::async_trait]
impl LightningNode for LdkNode {
    async fn open_channel(
        &self,
        peer_pubkey: &str,
        peer_addr: &str,
        local_sat: u64,
        push_msat: u64,
    ) -> Result<Channel, Error> {
        let push = if push_msat > 0 { Some(push_msat) } else { None };
        let response = self
            .client
            .open_channel(OpenChannelRequest {
                node_pubkey: peer_pubkey.to_string(),
                address: peer_addr.to_string(),
                channel_amount_sats: local_sat,
                push_to_counterparty_msat: push,
                announce_channel: false,
                channel_config: None,
            })
            .await
            .map_err(|e| Error::Rpc(e.message))?;

        Ok(Channel {
            id: response.user_channel_id,
            peer_pubkey: peer_pubkey.to_string(),
            local_balance_sat: local_sat,
            capacity_sat: local_sat,
            active: false,
        })
    }

    async fn close_channel(&self, channel: &Channel) -> Result<(), Error> {
        self.client
            .close_channel(CloseChannelRequest {
                user_channel_id: channel.id.clone(),
                counterparty_node_id: channel.peer_pubkey.clone(),
            })
            .await
            .map_err(|e| Error::Rpc(e.message))?;
        Ok(())
    }

    async fn force_close_channel(&self, channel: &Channel) -> Result<(), Error> {
        self.client
            .force_close_channel(ForceCloseChannelRequest {
                user_channel_id: channel.id.clone(),
                counterparty_node_id: channel.peer_pubkey.clone(),
                force_close_reason: None,
            })
            .await
            .map_err(|e| Error::Rpc(e.message))?;
        Ok(())
    }

    async fn list_channels(&self) -> Result<Vec<Channel>, Error> {
        let response = self
            .client
            .list_channels(ListChannelsRequest {})
            .await
            .map_err(|e| Error::Rpc(e.message))?;

        let channels = response
            .channels
            .into_iter()
            .map(|ch| Channel {
                id: ch.user_channel_id,
                peer_pubkey: ch.counterparty_node_id,
                local_balance_sat: ch.outbound_capacity_msat / 1000,
                capacity_sat: ch.channel_value_sats,
                active: ch.is_usable,
            })
            .collect();

        Ok(channels)
    }

    async fn create_invoice(&self, amount_msat: u64, description: &str) -> Result<String, Error> {
        let response = self
            .client
            .bolt11_receive(Bolt11ReceiveRequest {
                amount_msat: Some(amount_msat),
                description: Some(Bolt11InvoiceDescription {
                    kind: Some(bolt11_invoice_description::Kind::Direct(
                        description.to_string(),
                    )),
                }),
                expiry_secs: 3600,
            })
            .await
            .map_err(|e| Error::Rpc(e.message))?;

        Ok(response.invoice)
    }

    async fn send_payment(&self, bolt11: &str) -> Result<PaymentResult, Error> {
        let send_resp = self
            .client
            .bolt11_send(Bolt11SendRequest {
                invoice: bolt11.to_string(),
                amount_msat: None,
                route_parameters: None,
            })
            .await
            .map_err(|e| Error::Rpc(e.message))?;

        let payment_id = send_resp.payment_id;

        // Poll until the payment reaches a terminal state (up to 30 s).
        for _ in 0..60 {
            tokio::time::sleep(Duration::from_millis(500)).await;

            let details = self
                .client
                .get_payment_details(GetPaymentDetailsRequest {
                    payment_id: payment_id.clone(),
                })
                .await
                .map_err(|e| Error::Rpc(e.message))?;

            if let Some(payment) = details.payment {
                let status_val = payment.status;
                let (payment_hash, amount_msat_paid, fee_msat_paid) =
                    extract_payment_info(&payment);

                if status_val == LdkPaymentStatus::Succeeded as i32 {
                    return Ok(PaymentResult {
                        payment_hash,
                        status: PaymentStatus::Succeeded,
                        amount_msat: amount_msat_paid,
                        fee_msat: fee_msat_paid,
                    });
                } else if status_val == LdkPaymentStatus::Failed as i32 {
                    return Ok(PaymentResult {
                        payment_hash,
                        status: PaymentStatus::Failed,
                        amount_msat: amount_msat_paid,
                        fee_msat: fee_msat_paid,
                    });
                }
                // Pending — keep polling.
            }
        }

        Err(Error::Rpc(format!(
            "payment {payment_id} timed out after 30 s"
        )))
    }

    async fn new_address(&self) -> Result<String, Error> {
        let response = self
            .client
            .onchain_receive(OnchainReceiveRequest {})
            .await
            .map_err(|e| Error::Rpc(e.message))?;
        Ok(response.address)
    }
}

/// Extract `(payment_hash, amount_msat, fee_msat)` from an LDK payment proto.
fn extract_payment_info(
    payment: &ldk_server_client::ldk_server_protos::types::Payment,
) -> (String, u64, u64) {
    let payment_hash = match payment.kind.as_ref().and_then(|k| k.kind.as_ref()) {
        Some(payment_kind::Kind::Bolt11(b)) => b.hash.clone(),
        Some(payment_kind::Kind::Bolt11Jit(b)) => b.hash.clone(),
        Some(payment_kind::Kind::Bolt12Offer(b)) => b.hash.clone().unwrap_or_default(),
        Some(payment_kind::Kind::Bolt12Refund(b)) => b.hash.clone().unwrap_or_default(),
        Some(payment_kind::Kind::Spontaneous(b)) => b.hash.clone(),
        _ => String::new(),
    };

    let amount_msat = payment.amount_msat.unwrap_or(0);
    let fee_msat = payment.fee_paid_msat.unwrap_or(0);

    (payment_hash, amount_msat, fee_msat)
}
