//! Keys, install ids, signed requests and license tokens (docs/licensing-protocol.md).

use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use base64::Engine;
use ed25519_dalek::pkcs8::{DecodePrivateKey, EncodePrivateKey};
use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::io::Write;
use std::path::Path;

/// How far a request's timestamp may be from the server clock, in seconds.
pub const TIMESTAMP_WINDOW_SECS: i64 = 300;
/// Token lifetime: 30 days.
pub const TOKEN_LIFETIME_SECS: i64 = 30 * 24 * 60 * 60;
/// Every token carries `app` (the app is a one-time purchase); `relay` only while the
/// serial's relay subscription runs.
pub const FEATURE_APP: &str = "app";
pub const FEATURE_RELAY: &str = "relay";

pub fn b64url(bytes: &[u8]) -> String {
    URL_SAFE_NO_PAD.encode(bytes)
}

/// Decodes base64url without padding, accepting only the canonical encoding
/// (so the string that goes into the signed message is unambiguous).
pub fn b64url_decode_canonical(s: &str) -> Option<Vec<u8>> {
    let bytes = URL_SAFE_NO_PAD.decode(s).ok()?;
    (URL_SAFE_NO_PAD.encode(&bytes) == s).then_some(bytes)
}

/// The printed form of a public key: raw 32 bytes, standard base64.
pub fn public_key_std_b64(key: &VerifyingKey) -> String {
    STANDARD.encode(key.as_bytes())
}

/// `install_key` from the wire → verifying key.
pub fn parse_install_key(install_key: &str) -> Option<VerifyingKey> {
    let bytes: [u8; 32] = b64url_decode_canonical(install_key)?.try_into().ok()?;
    VerifyingKey::from_bytes(&bytes).ok()
}

/// Install id = `base64url_nopad(sha256(raw public key))`.
pub fn install_id(key: &VerifyingKey) -> String {
    b64url(&Sha256::digest(key.as_bytes()))
}

/// The message an install signs:
/// `"eggify-license-v1\n" + action + "\n" + install_key + "\n" + timestamp + "\n" + serial`.
pub fn request_message(action: &str, install_key: &str, timestamp: i64, serial: &str) -> String {
    format!("eggify-license-v1\n{action}\n{install_key}\n{timestamp}\n{serial}")
}

/// Signs a request the way a client does (used by tests and the fixture).
pub fn sign_request(key: &SigningKey, action: &str, timestamp: i64, serial: &str) -> String {
    let install_key = b64url(key.verifying_key().as_bytes());
    let msg = request_message(action, &install_key, timestamp, serial);
    b64url(&key.sign(msg.as_bytes()).to_bytes())
}

#[derive(Debug, PartialEq, Eq)]
pub enum RequestAuthError {
    StaleTimestamp,
    BadSignature,
}

/// Checks the signature, then the timestamp window: a stale timestamp is only reported for a
/// validly signed request (the caller does not count that as an abuse failure).
pub fn verify_request(
    key: &VerifyingKey,
    action: &str,
    install_key: &str,
    timestamp: i64,
    serial: &str,
    signature: &str,
    now: i64,
) -> Result<(), RequestAuthError> {
    let sig: [u8; 64] = b64url_decode_canonical(signature)
        .and_then(|b| b.try_into().ok())
        .ok_or(RequestAuthError::BadSignature)?;
    let msg = request_message(action, install_key, timestamp, serial);
    key.verify_strict(msg.as_bytes(), &Signature::from_bytes(&sig))
        .map_err(|_| RequestAuthError::BadSignature)?;
    if now.abs_diff(timestamp) > TIMESTAMP_WINDOW_SECS as u64 {
        return Err(RequestAuthError::StaleTimestamp);
    }
    Ok(())
}

/// HMAC-SHA256 (RFC 2104).
pub fn hmac_sha256(key: &[u8], msg: &[u8]) -> [u8; 32] {
    let mut k = [0u8; 64];
    if key.len() > 64 {
        k[..32].copy_from_slice(&Sha256::digest(key));
    } else {
        k[..key.len()].copy_from_slice(key);
    }
    let mut inner = Sha256::new();
    inner.update(k.map(|b| b ^ 0x36));
    inner.update(msg);
    let mut outer = Sha256::new();
    outer.update(k.map(|b| b ^ 0x5c));
    outer.update(inner.finalize());
    outer.finalize().into()
}

/// Keyed hash of a client block for the interest table: HMAC-SHA256 over the IPv4 address or
/// the IPv6 /64, keyed by a secret derived from the signing key with its own domain label, so
/// the stored value can't be reversed by hashing the address space.
pub fn interest_ip_hash(server_key: &SigningKey, ip: std::net::IpAddr) -> String {
    let mut h = Sha256::new();
    h.update(b"eggify-license-interest-ip-v1\n");
    h.update(server_key.to_bytes());
    let secret = h.finalize();
    let block = crate::abuse::Net::client(ip).to_string();
    hex::encode(hmac_sha256(&secret, block.as_bytes()))
}

/// License token payload. Field order is the serialisation order.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct TokenPayload {
    pub v: u32,
    pub serial_id: String,
    pub install: String,
    pub plan: String,
    pub features: Vec<String>,
    /// End of the relay subscription (unix seconds), or null.
    pub relay_until: Option<i64>,
    pub machine: String,
    pub issued_at: i64,
    pub expires_at: i64,
}

impl TokenPayload {
    pub fn new(
        serial_id: &str,
        install: &str,
        plan: &str,
        machine: &str,
        relay_until: Option<i64>,
        issued_at: i64,
    ) -> Self {
        let mut features = vec![FEATURE_APP.to_string()];
        if relay_until.is_some_and(|t| t > issued_at) {
            features.push(FEATURE_RELAY.to_string());
        }
        TokenPayload {
            v: 1,
            serial_id: serial_id.to_string(),
            install: install.to_string(),
            plan: plan.to_string(),
            features,
            relay_until,
            machine: machine.to_string(),
            issued_at,
            expires_at: issued_at + TOKEN_LIFETIME_SECS,
        }
    }

    pub fn has_feature(&self, f: &str) -> bool {
        self.features.iter().any(|x| x == f)
    }

    /// What the relay checks on top of a valid token: feature `relay` and `now < relay_until`.
    pub fn admits_relay(&self, now: i64) -> bool {
        self.has_feature(FEATURE_RELAY) && self.relay_until.is_some_and(|t| now < t)
    }
}

/// `base64url(payload JSON) + "." + base64url(signature over those JSON bytes)`.
pub fn sign_token(key: &SigningKey, payload: &TokenPayload) -> String {
    let json = serde_json::to_vec(payload).expect("payload serialises");
    let sig = key.sign(&json);
    format!("{}.{}", b64url(&json), b64url(&sig.to_bytes()))
}

#[derive(Debug, PartialEq, Eq)]
pub enum TokenError {
    Malformed,
    BadSignature,
    WrongVersion,
    WrongInstall,
    Expired,
}

/// Verifies a token the way the app and relay do: signature, `v` = 1, `install`, not expired.
pub fn verify_token(
    key: &VerifyingKey,
    token: &str,
    install: &str,
    now: i64,
) -> Result<TokenPayload, TokenError> {
    let (p, s) = token.split_once('.').ok_or(TokenError::Malformed)?;
    let json = URL_SAFE_NO_PAD
        .decode(p)
        .map_err(|_| TokenError::Malformed)?;
    let sig: [u8; 64] = URL_SAFE_NO_PAD
        .decode(s)
        .ok()
        .and_then(|b| b.try_into().ok())
        .ok_or(TokenError::Malformed)?;
    key.verify(&json, &Signature::from_bytes(&sig))
        .map_err(|_| TokenError::BadSignature)?;
    let payload: TokenPayload = serde_json::from_slice(&json).map_err(|_| TokenError::Malformed)?;
    if payload.v != 1 {
        return Err(TokenError::WrongVersion);
    }
    if payload.install != install {
        return Err(TokenError::WrongInstall);
    }
    if now >= payload.expires_at {
        return Err(TokenError::Expired);
    }
    Ok(payload)
}

pub fn load_signing_key(path: &Path) -> Result<SigningKey, String> {
    let pem = std::fs::read_to_string(path).map_err(|e| format!("read {}: {e}", path.display()))?;
    load_signing_key_from_pem(&pem).map_err(|e| format!("{}: {e}", path.display()))
}

pub fn load_signing_key_from_pem(pem: &str) -> Result<SigningKey, String> {
    SigningKey::from_pkcs8_pem(pem).map_err(|e| format!("parse PKCS#8 Ed25519 key: {e}"))
}

/// Writes a new PKCS#8 PEM key with mode 0600; refuses to overwrite.
pub fn write_new_signing_key(path: &Path) -> Result<SigningKey, String> {
    let key = SigningKey::generate(&mut rand::rngs::OsRng);
    let pem = key
        .to_pkcs8_pem(ed25519_dalek::pkcs8::spki::der::pem::LineEnding::LF)
        .map_err(|e| format!("encode key: {e}"))?;
    let mut opts = std::fs::OpenOptions::new();
    opts.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        opts.mode(0o600);
    }
    let mut f = opts
        .open(path)
        .map_err(|e| format!("create {}: {e}", path.display()))?;
    f.write_all(pem.as_bytes())
        .and_then(|_| f.sync_all())
        .map_err(|e| format!("write {}: {e}", path.display()))?;
    Ok(key)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn key(seed: u8) -> SigningKey {
        SigningKey::from_bytes(&[seed; 32])
    }

    #[test]
    fn token_round_trip_and_fields() {
        let server = key(1);
        let install = install_id(&key(2).verifying_key());
        let p = TokenPayload::new(
            "8f6c-id",
            &install,
            "standard",
            "Mac",
            Some(1_800_000_000),
            1_792_000_000,
        );
        let t = sign_token(&server, &p);
        let got = verify_token(&server.verifying_key(), &t, &install, 1_792_000_001).unwrap();
        assert_eq!(got, p);
        assert_eq!(got.v, 1);
        assert_eq!(got.features, vec!["app", "relay"]);
        assert_eq!(got.expires_at, 1_792_000_000 + 30 * 86400);
        let json: serde_json::Value = serde_json::from_slice(
            &URL_SAFE_NO_PAD
                .decode(t.split('.').next().unwrap())
                .unwrap(),
        )
        .unwrap();
        assert_eq!(json["relay_until"], 1_800_000_000);
        for f in [
            "relay_until",
            "v",
            "serial_id",
            "install",
            "plan",
            "features",
            "machine",
            "issued_at",
            "expires_at",
        ] {
            assert!(json.get(f).is_some(), "field {f}");
        }
    }

    #[test]
    fn relay_feature_follows_the_subscription() {
        let t = 1_792_000_000;
        let none = TokenPayload::new("id", "i", "standard", "", None, t);
        assert_eq!(none.features, vec!["app"]);
        assert!(!none.admits_relay(t));
        let ended = TokenPayload::new("id", "i", "standard", "", Some(t), t);
        assert_eq!(ended.features, vec!["app"]);
        let running = TokenPayload::new("id", "i", "standard", "", Some(t + 10 * 86400), t);
        assert_eq!(running.features, vec!["app", "relay"]);
        assert_eq!(
            running.expires_at,
            t + 30 * 86400,
            "token lifetime is independent of the relay"
        );
        assert!(running.admits_relay(t + 10 * 86400 - 1));
        assert!(
            !running.admits_relay(t + 10 * 86400),
            "relay stops at relay_until, token still valid"
        );
        // A null relay_until serialises as null, not missing.
        let v: serde_json::Value = serde_json::to_value(&none).unwrap();
        assert!(v.get("relay_until").unwrap().is_null());
    }

    #[test]
    fn token_tampering_fails() {
        let server = key(1);
        let install = install_id(&key(2).verifying_key());
        let p = TokenPayload::new("id", &install, "standard", "Mac", None, 1_792_000_000);
        let t = sign_token(&server, &p);
        let (pl, sig) = t.split_once('.').unwrap();
        let vk = server.verifying_key();

        let mut forged = p.clone();
        forged.expires_at += 1000;
        let forged_pl = b64url(&serde_json::to_vec(&forged).unwrap());
        assert_eq!(
            verify_token(&vk, &format!("{forged_pl}.{sig}"), &install, 0),
            Err(TokenError::BadSignature)
        );

        let mut sb = URL_SAFE_NO_PAD.decode(sig).unwrap();
        sb[0] ^= 1;
        assert_eq!(
            verify_token(&vk, &format!("{pl}.{}", b64url(&sb)), &install, 0),
            Err(TokenError::BadSignature)
        );

        assert_eq!(
            verify_token(&key(3).verifying_key(), &t, &install, 0),
            Err(TokenError::BadSignature)
        );
        assert_eq!(
            verify_token(&vk, &t, "other", 0),
            Err(TokenError::WrongInstall)
        );
        assert_eq!(
            verify_token(&vk, &t, &install, p.expires_at),
            Err(TokenError::Expired)
        );
        assert_eq!(
            verify_token(&vk, "nodot", &install, 0),
            Err(TokenError::Malformed)
        );
    }

    #[test]
    fn request_signature_window_and_tamper() {
        let ik = key(5);
        let wire_key = b64url(ik.verifying_key().as_bytes());
        let vk = parse_install_key(&wire_key).unwrap();
        let sig = sign_request(&ik, "activate", 1000, "7H3K9Q2M4X8R6T1V5N0T");
        assert_eq!(
            verify_request(
                &vk,
                "activate",
                &wire_key,
                1000,
                "7H3K9Q2M4X8R6T1V5N0T",
                &sig,
                1300
            ),
            Ok(())
        );
        assert_eq!(
            verify_request(
                &vk,
                "activate",
                &wire_key,
                1000,
                "7H3K9Q2M4X8R6T1V5N0T",
                &sig,
                1301
            ),
            Err(RequestAuthError::StaleTimestamp)
        );
        assert_eq!(
            verify_request(
                &vk,
                "renew",
                &wire_key,
                1000,
                "7H3K9Q2M4X8R6T1V5N0T",
                &sig,
                1000
            ),
            Err(RequestAuthError::BadSignature)
        );
        assert_eq!(
            verify_request(&vk, "activate", &wire_key, 1000, "", &sig, 1000),
            Err(RequestAuthError::BadSignature)
        );
        assert_eq!(
            verify_request(
                &vk,
                "activate",
                &wire_key,
                1000,
                "7H3K9Q2M4X8R6T1V5N0T",
                "garbage",
                1000
            ),
            Err(RequestAuthError::BadSignature)
        );
    }

    #[test]
    fn hmac_matches_rfc4231_case_2() {
        assert_eq!(
            hex::encode(hmac_sha256(b"Jefe", b"what do ya want for nothing?")),
            "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
        );
    }

    #[test]
    fn interest_ip_hash_is_keyed_and_uses_the_client_block() {
        let (a, b) = (key(1), key(2));
        let ip = |s: &str| s.parse::<std::net::IpAddr>().unwrap();
        let plain = hex::encode(Sha256::digest(b"203.0.113.9"));
        assert_ne!(interest_ip_hash(&a, ip("203.0.113.9")), plain);
        assert_ne!(
            interest_ip_hash(&a, ip("203.0.113.9")),
            interest_ip_hash(&b, ip("203.0.113.9"))
        );
        assert_eq!(
            interest_ip_hash(&a, ip("2001:db8:1:2::1")),
            interest_ip_hash(&a, ip("2001:db8:1:2:ffff::9"))
        );
        assert_ne!(
            interest_ip_hash(&a, ip("2001:db8:1:2::1")),
            interest_ip_hash(&a, ip("2001:db8:1:3::1"))
        );
    }

    #[test]
    fn signature_is_checked_before_the_timestamp() {
        let ik = key(5);
        let wire_key = b64url(ik.verifying_key().as_bytes());
        let vk = parse_install_key(&wire_key).unwrap();
        // Stale and badly signed: reported as a bad signature (an abuse failure).
        assert_eq!(
            verify_request(&vk, "renew", &wire_key, 1000, "", "garbage", 9000),
            Err(RequestAuthError::BadSignature)
        );
        // Extreme timestamps do not overflow.
        let sig = sign_request(&ik, "renew", i64::MIN, "");
        assert_eq!(
            verify_request(&vk, "renew", &wire_key, i64::MIN, "", &sig, i64::MAX),
            Err(RequestAuthError::StaleTimestamp)
        );
    }

    #[test]
    fn install_key_must_be_canonical_32_bytes() {
        let wire = b64url(key(5).verifying_key().as_bytes());
        assert!(parse_install_key(&wire).is_some());
        assert!(parse_install_key(&format!("{wire}=")).is_none());
        assert!(parse_install_key(&STANDARD.encode(key(5).verifying_key().as_bytes())).is_none());
        assert!(parse_install_key("AAAA").is_none());
    }

    #[test]
    fn keygen_writes_0600_and_refuses_overwrite() {
        let dir = tempfile::tempdir().unwrap();
        let p = dir.path().join("k.pem");
        let k = write_new_signing_key(&p).unwrap();
        assert_eq!(load_signing_key(&p).unwrap().to_bytes(), k.to_bytes());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                std::fs::metadata(&p).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
        assert!(write_new_signing_key(&p).is_err());
    }
}
