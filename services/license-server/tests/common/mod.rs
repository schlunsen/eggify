#![allow(dead_code)]
//! Test helpers that build requests the way a client does, without the server's own helpers,
//! so the wire format is pinned independently.

use axum::body::Body;
use axum::extract::ConnectInfo;
use axum::http::{Request, Response, StatusCode};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use eggify_license::{api, db};
use http_body_util::BodyExt;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::net::{Ipv4Addr, SocketAddr};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicI64, AtomicU32, Ordering};
use std::sync::Arc;
use tower::ServiceExt;

pub fn now() -> i64 {
    api::unix_now()
}

pub struct Client {
    pub key: SigningKey,
}

impl Client {
    pub fn new(seed: u8) -> Self {
        Client {
            key: SigningKey::from_bytes(&[seed; 32]),
        }
    }
    pub fn from_seed(seed: [u8; 32]) -> Self {
        Client {
            key: SigningKey::from_bytes(&seed),
        }
    }
    pub fn install_key(&self) -> String {
        URL_SAFE_NO_PAD.encode(self.key.verifying_key().as_bytes())
    }
    pub fn install_id(&self) -> String {
        URL_SAFE_NO_PAD.encode(Sha256::digest(self.key.verifying_key().as_bytes()))
    }
    pub fn message(&self, action: &str, timestamp: i64, serial: &str) -> String {
        format!(
            "eggify-license-v1\n{action}\n{}\n{timestamp}\n{serial}",
            self.install_key()
        )
    }
    pub fn sign(&self, action: &str, timestamp: i64, serial: &str) -> String {
        URL_SAFE_NO_PAD.encode(
            self.key
                .sign(self.message(action, timestamp, serial).as_bytes())
                .to_bytes(),
        )
    }
    /// `serial_typed` goes in the body; `serial_norm` into the signed message.
    pub fn activate_body(
        &self,
        serial_typed: &str,
        serial_norm: &str,
        ts: i64,
        machine: &str,
    ) -> Value {
        json!({
            "serial": serial_typed,
            "install_key": self.install_key(),
            "timestamp": ts,
            "signature": self.sign("activate", ts, serial_norm),
            "machine": machine,
            "app_version": "2.18.0",
        })
    }
    pub fn renew_body(&self, ts: i64) -> Value {
        json!({ "install_key": self.install_key(), "timestamp": ts, "signature": self.sign("renew", ts, ""), "app_version": "2.18.1" })
    }
    pub fn deactivate_body(&self, ts: i64) -> Value {
        json!({ "install_key": self.install_key(), "timestamp": ts, "signature": self.sign("deactivate", ts, "") })
    }
}

/// Normalised form of a formatted serial (test-side, no server code).
pub fn norm(formatted: &str) -> String {
    formatted.trim_start_matches("EGG-").replace('-', "")
}

pub struct Harness {
    pub _dir: tempfile::TempDir,
    pub db_path: PathBuf,
    key_path: PathBuf,
    pub server_key: SigningKey,
    pub state: api::AppState,
    pub app: axum::Router,
    /// Set when the server clock is injected (`with_clock`); `set_time` moves it.
    pub clock: Option<Arc<AtomicI64>>,
    limits: api::Limits,
}

static NEXT_IP: AtomicU32 = AtomicU32::new(1);

/// A fresh client address per call, so tests that aren't about bans don't trip them.
pub fn fresh_peer() -> String {
    let n = NEXT_IP.fetch_add(1, Ordering::Relaxed);
    format!("{}:40000", Ipv4Addr::from(0x6440_0000u32 + n)) // 100.64.0.0/10
}

impl Harness {
    pub fn new() -> Self {
        Self::build(None, api::Limits::default())
    }

    pub fn with_clock(t: i64) -> Self {
        Self::build(Some(t), api::Limits::default())
    }

    pub fn with_limits(limits: api::Limits) -> Self {
        Self::build(None, limits)
    }

    fn build(clock: Option<i64>, limits: api::Limits) -> Self {
        let dir = tempfile::tempdir().unwrap();
        let db_path = dir.path().join("license.db");
        let key_path = dir.path().join("signing.pem");
        let server_key = eggify_license::crypto::write_new_signing_key(&key_path).unwrap();
        let clock = clock.map(|t| Arc::new(AtomicI64::new(t)));
        let (state, app) = Self::instance(&db_path, &key_path, clock.clone(), limits);
        Harness {
            _dir: dir,
            db_path,
            key_path,
            server_key,
            state,
            app,
            clock,
            limits,
        }
    }

    fn instance(
        db_path: &Path,
        key_path: &Path,
        clock: Option<Arc<AtomicI64>>,
        limits: api::Limits,
    ) -> (api::AppState, axum::Router) {
        let conn = db::open(db_path).unwrap();
        let key = eggify_license::crypto::load_signing_key(key_path).unwrap();
        let clock: api::Clock = match clock {
            Some(c) => Arc::new(move || c.load(Ordering::SeqCst)),
            None => Arc::new(api::unix_now),
        };
        let state = api::AppState::with_options(conn, key, clock, limits)
            .with_interest_origin(Some("https://example.com"));
        (state.clone(), api::router(state))
    }

    /// A new server instance on the same database and key (a restart).
    pub fn restart(&mut self) {
        let (state, app) = Self::instance(
            &self.db_path,
            &self.key_path,
            self.clock.clone(),
            self.limits,
        );
        self.state = state;
        self.app = app;
    }

    /// The server's current time.
    pub fn time(&self) -> i64 {
        self.clock
            .as_ref()
            .map_or_else(now, |c| c.load(Ordering::SeqCst))
    }

    pub fn set_time(&self, t: i64) {
        self.clock
            .as_ref()
            .expect("harness built with_clock")
            .store(t, Ordering::SeqCst);
    }

    pub fn admin_db(&self) -> rusqlite::Connection {
        db::open(&self.db_path).unwrap()
    }

    /// Creates a serial (with 12 months of relay) through the admin path; returns (formatted, id).
    pub fn serial(&self, seats: i64) -> (String, String) {
        self.serial_relay(seats, 12)
    }

    pub fn serial_relay(&self, seats: i64, relay_months: u32) -> (String, String) {
        eggify_license::admin::create_serial(
            &self.admin_db(),
            seats,
            "standard",
            "test",
            relay_months,
        )
        .unwrap()
    }

    pub async fn send(&self, mut req: Request<Body>, peer: &str) -> Response<Body> {
        let addr: SocketAddr = peer.parse().unwrap();
        req.extensions_mut().insert(ConnectInfo(addr));
        self.app.clone().oneshot(req).await.unwrap()
    }

    /// Full response: status, Retry-After (if any), JSON body.
    pub async fn call_full(
        &self,
        method: &str,
        path: &str,
        body: Option<&Value>,
        peer: &str,
        xff: Option<&str>,
    ) -> (StatusCode, Option<i64>, Value) {
        let mut b = Request::builder()
            .method(method)
            .uri(path)
            .header("content-type", "application/json");
        if let Some(x) = xff {
            b = b.header("x-forwarded-for", x);
        }
        let req = b
            .body(match body {
                Some(v) => Body::from(serde_json::to_vec(v).unwrap()),
                None => Body::empty(),
            })
            .unwrap();
        let resp = self.send(req, peer).await;
        let status = resp.status();
        let retry = resp
            .headers()
            .get("retry-after")
            .map(|v| v.to_str().unwrap().parse::<i64>().unwrap());
        let bytes = resp.into_body().collect().await.unwrap().to_bytes();
        let v = if bytes.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&bytes)
                .unwrap_or(Value::String(String::from_utf8_lossy(&bytes).into()))
        };
        (status, retry, v)
    }

    pub async fn call(
        &self,
        method: &str,
        path: &str,
        body: Option<&Value>,
        peer: &str,
        xff: Option<&str>,
    ) -> (StatusCode, Value) {
        let (st, _, v) = self.call_full(method, path, body, peer, xff).await;
        (st, v)
    }

    /// POST from a fresh client address.
    pub async fn post(&self, path: &str, body: &Value) -> (StatusCode, Value) {
        self.call("POST", path, Some(body), &fresh_peer(), None)
            .await
    }

    pub async fn post_raw(&self, path: &str, raw: &str) -> (StatusCode, Value) {
        let req = Request::builder()
            .method("POST")
            .uri(path)
            .header("content-type", "application/json")
            .body(Body::from(raw.to_string()))
            .unwrap();
        let resp = self.send(req, &fresh_peer()).await;
        let status = resp.status();
        let bytes = resp.into_body().collect().await.unwrap().to_bytes();
        (
            status,
            serde_json::from_slice(&bytes).unwrap_or(Value::Null),
        )
    }
}

/// Decodes and verifies a token with the given server key, test-side.
pub fn decode_token(server: &SigningKey, token: &str) -> Value {
    let (p, s) = token.split_once('.').unwrap();
    let payload = URL_SAFE_NO_PAD.decode(p).unwrap();
    let sig: [u8; 64] = URL_SAFE_NO_PAD.decode(s).unwrap().try_into().unwrap();
    server
        .verifying_key()
        .verify_strict(&payload, &ed25519_dalek::Signature::from_bytes(&sig))
        .expect("token signature verifies");
    serde_json::from_slice(&payload).unwrap()
}
