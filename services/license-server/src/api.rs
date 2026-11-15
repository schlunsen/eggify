//! HTTP API: `/v1/activate`, `/v1/renew`, `/v1/deactivate`, `/v1/health`.

use crate::abuse::{self, Abuse};
use crate::crypto::{self, RequestAuthError, TokenPayload};
use crate::db;
use crate::ratelimit::{Class, RateLimiter};
use crate::serial;
use axum::body::{Body, Bytes};
use axum::extract::{ConnectInfo, Extension, Request, State};
use axum::http::{header, HeaderMap, HeaderValue, Method, StatusCode};
use axum::middleware::{self, Next};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use ed25519_dalek::{SigningKey, VerifyingKey};
use rusqlite::Connection;
use serde::Deserialize;
use serde_json::{json, Value};
use std::borrow::Cow;
use std::collections::HashMap;
use std::net::{IpAddr, Ipv4Addr, SocketAddr};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::sync::Semaphore;

pub const MAX_BODY_BYTES: usize = 16 * 1024;
/// Request counters older than this are dropped from `request_stats`.
const STATS_KEEP_SECS: i64 = 30 * 24 * 60 * 60;
const MAX_MACHINE_CHARS: usize = 100;
const MAX_VERSION_CHARS: usize = 64;

pub type Clock = Arc<dyn Fn() -> i64 + Send + Sync>;

/// Server-wide request limits.
#[derive(Debug, Clone, Copy)]
pub struct Limits {
    /// Whole request, body included.
    pub timeout: Duration,
    /// Requests being handled at once; more get 503 `busy`.
    pub max_in_flight: usize,
    /// Requests one client may have in flight at once; more get 503 `busy`.
    pub max_per_client: usize,
    /// Reading the request body (slow senders get 503 `timeout`).
    pub body_timeout: Duration,
}

impl Default for Limits {
    fn default() -> Self {
        Limits {
            timeout: Duration::from_secs(10),
            max_in_flight: 256,
            max_per_client: 8,
            body_timeout: Duration::from_secs(3),
        }
    }
}

#[derive(Clone)]
pub struct AppState {
    inner: Arc<Inner>,
}

struct Inner {
    db: Mutex<Connection>,
    key: SigningKey,
    limiter: RateLimiter,
    abuse: Abuse,
    clock: Clock,
    limits: Limits,
    in_flight: Arc<Semaphore>,
    /// Requests currently being handled, per client block.
    per_client: Arc<Mutex<HashMap<abuse::Net, usize>>>,
    /// The website origin allowed to call `/v1/interest` from a browser (CORS); none = no origin.
    interest_origin: Option<HeaderValue>,
}

/// Releases a client's in-flight slot when the request ends (or is dropped).
struct ClientSlot {
    map: Arc<Mutex<HashMap<abuse::Net, usize>>>,
    net: abuse::Net,
}

impl Drop for ClientSlot {
    fn drop(&mut self) {
        let mut m = self.map.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(n) = m.get_mut(&self.net) {
            *n -= 1;
            if *n == 0 {
                m.remove(&self.net);
            }
        }
    }
}

/// The client address the guard middleware worked out (see `client_ip`).
#[derive(Debug, Clone, Copy)]
pub struct ClientIp(pub IpAddr);

pub fn unix_now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

impl AppState {
    pub fn new(db: Connection, key: SigningKey) -> Self {
        Self::with_clock(db, key, Arc::new(unix_now))
    }

    pub fn with_clock(db: Connection, key: SigningKey, clock: Clock) -> Self {
        Self::with_options(db, key, clock, Limits::default())
    }

    /// Loads the persisted bans, so a restart doesn't lift them.
    pub fn with_options(db: Connection, key: SigningKey, clock: Clock, limits: Limits) -> Self {
        let abuse = Abuse::new();
        match db::load_bans(&db) {
            Ok(bans) => abuse.load_bans(bans),
            Err(e) => eprintln!("loading bans: {e}"),
        }
        AppState {
            inner: Arc::new(Inner {
                db: Mutex::new(db),
                key,
                limiter: RateLimiter::new(),
                abuse,
                clock,
                limits,
                in_flight: Arc::new(Semaphore::new(limits.max_in_flight)),
                per_client: Arc::new(Mutex::new(HashMap::new())),
                interest_origin: None,
            }),
        }
    }

    /// Sets the website origin (for example `https://example.com`) that may call
    /// `/v1/interest` from a browser. Without it no origin gets CORS headers.
    /// Call right after construction, before the state is cloned.
    pub fn with_interest_origin(mut self, origin: Option<&str>) -> Self {
        let origin = origin
            .map(|o| o.trim().trim_end_matches('/'))
            .filter(|o| !o.is_empty())
            .and_then(|o| HeaderValue::from_str(o).ok());
        Arc::get_mut(&mut self.inner)
            .expect("with_interest_origin is called before the state is shared")
            .interest_origin = origin;
        self
    }

    fn client_slot(&self, ip: IpAddr) -> Option<ClientSlot> {
        let net = abuse::Net::client(ip);
        let mut m = self
            .inner
            .per_client
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        let n = m.entry(net).or_insert(0);
        if *n >= self.inner.limits.max_per_client {
            return None;
        }
        *n += 1;
        Some(ClientSlot {
            map: self.inner.per_client.clone(),
            net,
        })
    }

    /// Logs `line` unless the same `key` was logged within `ttl` seconds.
    fn log_throttled(&self, key: &str, ttl: i64, line: impl FnOnce() -> String) {
        if self.inner.abuse.should_log(key, ttl, self.now()) {
            println!("{}", line());
        }
    }

    /// Periodic housekeeping (every 30 s in `serve`): drops ended bans whose count is no longer
    /// remembered, reloads the ban cache from the database (picks up `ban add|remove`), flushes
    /// request counters, prunes rate-limit buckets and failure windows, re-checks the brake.
    pub async fn maintenance(&self) {
        let now = self.now();
        let st = self.clone();
        let _ = tokio::task::spawn_blocking(move || {
            let mut conn = st.inner.db.lock().unwrap_or_else(|e| e.into_inner());
            if let Err(e) = st.flush_locked(&mut conn, now) {
                eprintln!("flushing stats: {e}");
            }
            if let Err(e) = db::cleanup_bans(&conn, now) {
                eprintln!("cleaning up bans: {e}");
            }
            // Bans whose write failed earlier are retried first; the reload below keeps the
            // ones that still fail.
            for ban in st.inner.abuse.unsaved() {
                if db::upsert_ban(&conn, &ban, now).is_ok() {
                    st.inner.abuse.mark_saved(&ban.net);
                }
            }
            match db::load_bans(&conn) {
                Ok(bans) => st.inner.abuse.load_bans(bans),
                Err(e) => eprintln!("reloading bans: {e}"),
            }
        })
        .await;
        self.inner.limiter.prune();
        self.inner.abuse.prune(now);
    }

    /// Writes the in-memory request counters (also called on shutdown).
    pub async fn flush(&self) {
        let now = self.now();
        let st = self.clone();
        let _ = tokio::task::spawn_blocking(move || {
            let mut conn = st.inner.db.lock().unwrap_or_else(|e| e.into_inner());
            if let Err(e) = st.flush_locked(&mut conn, now) {
                eprintln!("flushing stats: {e}");
            }
        })
        .await;
    }

    fn flush_locked(&self, conn: &mut Connection, now: i64) -> rusqlite::Result<()> {
        let counters = self.inner.abuse.take_counters();
        if !counters.is_empty() {
            db::add_stats(conn, &counters)?;
        }
        db::prune_stats(conn, now - STATS_KEEP_SECS)?;
        Ok(())
    }

    /// Records a request's outcome: failures count towards a ban (persisted under the database
    /// lock, so a concurrent cache reload can't lose it; the current ban row is re-read there),
    /// a completed activate or renew clears the failure window.
    async fn finish(&self, ip: IpAddr, action: &'static str, outcome: Result<(), &'static str>) {
        let now = self.now();
        match outcome {
            Ok(()) => {
                if matches!(action, "activate" | "renew") {
                    self.inner.abuse.clear_failures(ip);
                }
                self.inner.abuse.count(&format!("{action}_ok"), now);
            }
            Err(code) if abuse::is_failure(code) => {
                let st = self.clone();
                let _ = tokio::task::spawn_blocking(move || {
                    let conn = st.inner.db.lock().unwrap_or_else(|e| e.into_inner());
                    let ban = st.inner.abuse.record_failure(ip, code, now, |net| {
                        db::load_bans(&conn)
                            .ok()
                            .and_then(|bans| bans.into_iter().find(|b| b.net == *net))
                    });
                    if let Some(ban) = ban {
                        if let Err(e) = db::upsert_ban(&conn, &ban, now) {
                            eprintln!("saving ban (kept in memory, will retry): {e}");
                            st.inner.abuse.mark_unsaved(ban);
                        }
                    }
                })
                .await;
            }
            Err(code) => self.inner.abuse.count(code, now),
        }
    }

    fn now(&self) -> i64 {
        (self.inner.clock)()
    }

    /// Runs `f` with the database on the blocking pool.
    async fn with_db<T: Send + 'static>(
        &self,
        f: impl FnOnce(&mut Connection, &SigningKey) -> T + Send + 'static,
    ) -> Result<T, ApiError> {
        let st = self.clone();
        tokio::task::spawn_blocking(move || {
            let mut conn = st.inner.db.lock().unwrap_or_else(|e| e.into_inner());
            f(&mut conn, &st.inner.key)
        })
        .await
        .map_err(|_| ApiError::internal())
    }
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/v1/activate", post(activate))
        .route("/v1/renew", post(renew))
        .route("/v1/deactivate", post(deactivate))
        .route("/v1/interest", post(interest))
        .route("/v1/health", get(health))
        .fallback(|| async {
            ApiError::new(StatusCode::NOT_FOUND, "not_found", "There is nothing here.")
        })
        // Outermost last: in-flight cap + timeout, then the ban check.
        .layer(middleware::from_fn_with_state(state.clone(), guard))
        .layer(middleware::from_fn_with_state(state.clone(), limit))
        // Outermost of all, so preflights are answered before routing and the rate limit.
        .layer(middleware::from_fn_with_state(state.clone(), cors))
        .with_state(state)
}

/// Caps concurrent requests and bounds each one (body included) by the timeout.
async fn limit(State(state): State<AppState>, req: Request, next: Next) -> Response {
    let Ok(_permit) = state.inner.in_flight.clone().try_acquire_owned() else {
        state.inner.abuse.count("overloaded", state.now());
        state.log_throttled("overloaded", 60, || {
            format!(
                "{} busy in_flight_cap (further ones within 60s not logged)",
                req.uri().path()
            )
        });
        return ApiError::overloaded().into_response();
    };
    match tokio::time::timeout(state.inner.limits.timeout, next.run(req)).await {
        Ok(resp) => resp,
        Err(_) => ApiError::timeout().into_response(),
    }
}

/// Works out the client address and refuses banned clients on every route but health,
/// with nothing more than the in-memory ban lookup.
async fn guard(State(state): State<AppState>, mut req: Request, next: Next) -> Response {
    let peer = req
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .map(|c| c.0);
    let ip = client_ip(peer, req.headers());
    req.extensions_mut().insert(ClientIp(ip));
    if req.uri().path() != "/v1/health" {
        let now = state.now();
        if let Some(retry) = state.inner.abuse.banned(ip, now) {
            state.inner.abuse.count("temporarily_blocked", now);
            // One line per ban: the quiet period lasts as long as the ban.
            state.log_throttled(&format!("blocked:{}", abuse::Net::client(ip)), retry, || {
                format!(
                    "{} temporarily_blocked ip={ip} retry_after={retry}s (further requests not logged)",
                    req.uri().path()
                )
            });
            return ApiError::temporarily_blocked(retry).into_response();
        }
        let Some(_slot) = state.client_slot(ip) else {
            state.inner.abuse.count("client_busy", now);
            state.log_throttled(
                &format!("client_busy:{}", abuse::Net::client(ip)),
                60,
                || {
                    format!(
                        "{} busy client_in_flight_cap ip={ip} (further ones within 60s not logged)",
                        req.uri().path()
                    )
                },
            );
            return ApiError::overloaded().into_response();
        };
        return next.run(req).await;
    }
    next.run(req).await
}

// ---------- errors ----------

#[derive(Debug)]
pub struct ApiError {
    status: StatusCode,
    code: &'static str,
    message: Cow<'static, str>,
    extra: Option<(&'static str, Value)>,
    retry_after: Option<i64>,
}

/// "about 45 minutes", "about 2 hours", "about 3 days".
fn human_wait(secs: i64) -> String {
    let mins = (secs + 59) / 60;
    if mins < 90 {
        let m = mins.max(1);
        format!("about {m} minute{}", if m == 1 { "" } else { "s" })
    } else if mins < 48 * 60 {
        format!("about {} hours", (mins + 30) / 60)
    } else {
        format!("about {} days", (mins + 12 * 60) / (24 * 60))
    }
}

impl ApiError {
    fn new(status: StatusCode, code: &'static str, message: impl Into<Cow<'static, str>>) -> Self {
        ApiError {
            status,
            code,
            message: message.into(),
            extra: None,
            retry_after: None,
        }
    }
    fn retry_after(mut self, secs: i64) -> Self {
        self.retry_after = Some(secs.max(1));
        self
    }
    fn temporarily_blocked(retry: i64) -> Self {
        Self::new(
            StatusCode::TOO_MANY_REQUESTS,
            "temporarily_blocked",
            format!(
                "Too many failed attempts came from your network, so it is blocked for now. Try again in {}.",
                human_wait(retry)
            ),
        )
        .retry_after(retry)
    }
    fn busy(retry: i64) -> Self {
        Self::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "busy",
            "Too many failed activations right now; try again later.",
        )
        .retry_after(retry)
    }
    fn overloaded() -> Self {
        Self::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "busy",
            "The license server is busy right now; try again in a moment.",
        )
        .retry_after(5)
    }
    fn timeout() -> Self {
        Self::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "timeout",
            "The request took too long. Try again.",
        )
        .retry_after(5)
    }
    fn invalid_email() -> Self {
        Self::new(
            StatusCode::BAD_REQUEST,
            "invalid_email",
            "That email address doesn't look right.",
        )
    }
    fn unsupported_media_type() -> Self {
        Self::new(
            StatusCode::UNSUPPORTED_MEDIA_TYPE,
            "unsupported_media_type",
            "Requests must be sent as application/json.",
        )
    }
    fn payload_too_large() -> Self {
        Self::new(
            StatusCode::PAYLOAD_TOO_LARGE,
            "payload_too_large",
            "The request is too large.",
        )
    }
    fn bad_request() -> Self {
        Self::new(
            StatusCode::BAD_REQUEST,
            "bad_request",
            "The request was not understood. Please update Eggify and try again.",
        )
    }
    fn invalid_serial() -> Self {
        Self::new(
            StatusCode::BAD_REQUEST,
            "invalid_serial",
            "That is not a valid serial number. Check it for typos.",
        )
    }
    fn stale_timestamp() -> Self {
        Self::new(StatusCode::UNAUTHORIZED, "stale_timestamp", "This computer's clock is off by more than 5 minutes. Set the date and time automatically and try again.")
    }
    fn bad_signature() -> Self {
        Self::new(
            StatusCode::UNAUTHORIZED,
            "bad_signature",
            "The request could not be verified.",
        )
    }
    fn unknown_serial() -> Self {
        Self::new(
            StatusCode::NOT_FOUND,
            "unknown_serial",
            "That serial number does not exist. Check it for typos.",
        )
    }
    fn serial_revoked() -> Self {
        Self::new(
            StatusCode::FORBIDDEN,
            "serial_revoked",
            "This serial number is no longer valid.",
        )
    }
    fn not_activated() -> Self {
        Self::new(
            StatusCode::NOT_FOUND,
            "not_activated",
            "This computer is not activated. Enter your serial number again.",
        )
    }
    fn rate_limited() -> Self {
        Self::new(
            StatusCode::TOO_MANY_REQUESTS,
            "rate_limited",
            "Too many attempts. Wait a minute and try again.",
        )
        .retry_after(60)
    }
    fn internal() -> Self {
        Self::new(
            StatusCode::INTERNAL_SERVER_ERROR,
            "internal",
            "Something went wrong on the license server. Try again later.",
        )
    }
    fn seats_full(activations: Value) -> Self {
        ApiError {
            extra: Some(("activations", activations)),
            ..Self::new(StatusCode::CONFLICT, "seats_full", "This serial number is already active on all its computers. Deactivate one of them first.")
        }
    }
}

impl From<rusqlite::Error> for ApiError {
    fn from(e: rusqlite::Error) -> Self {
        eprintln!("database error: {e}");
        ApiError::internal()
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        let mut body = json!({ "error": self.code, "message": self.message });
        if let Some((k, v)) = self.extra {
            body[k] = v;
        }
        let mut resp = (self.status, Json(body)).into_response();
        if let Some(secs) = self.retry_after {
            resp.headers_mut()
                .insert(header::RETRY_AFTER, HeaderValue::from(secs));
        }
        resp
    }
}

// ---------- request plumbing ----------

/// Client address: the peer, or (only when the peer is loopback, i.e. Caddy) the last
/// `X-Forwarded-For` entry, which is the one the proxy itself added.
pub fn client_ip(peer: Option<SocketAddr>, headers: &HeaderMap) -> IpAddr {
    let peer_ip = peer
        .map(|p| p.ip())
        .unwrap_or(IpAddr::V4(Ipv4Addr::UNSPECIFIED));
    let peer_ip = match peer_ip {
        IpAddr::V6(v6) => v6
            .to_ipv4_mapped()
            .map(IpAddr::V4)
            .unwrap_or(IpAddr::V6(v6)),
        v4 => v4,
    };
    if !peer_ip.is_loopback() {
        return peer_ip;
    }
    headers
        .get_all("x-forwarded-for")
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(','))
        .last()
        .and_then(|s| s.trim().parse::<IpAddr>().ok())
        .unwrap_or(peer_ip)
}

fn begin(state: &AppState, ip: IpAddr, action: &str, class: Class) -> Result<(), ApiError> {
    let now = state.now();
    if !state.inner.limiter.check(ip, class) {
        state.inner.abuse.count("rate_limited", now);
        // The first one per client and minute; the counter in `stats` has the rest.
        state.log_throttled(
            &format!("rate_limited:{action}:{}", abuse::Net::client(ip)),
            60,
            || format!("{action} rate_limited ip={ip} (further ones within 60s not logged)"),
        );
        return Err(ApiError::rate_limited());
    }
    if class == Class::Activate {
        if let Some(retry) = state.inner.abuse.brake(now) {
            state.inner.abuse.count("busy", now);
            state.log_throttled("busy:activate", 60, || {
                format!("{action} busy ip={ip} (further ones within 60s not logged)")
            });
            return Err(ApiError::busy(retry));
        }
    }
    Ok(())
}

/// Refuses anything but `application/json` (415). A browser can send `text/plain` cross-site
/// without a CORS preflight, so this is what keeps other websites from posting to the API.
fn require_json(headers: &HeaderMap) -> Result<(), ApiError> {
    let ok = headers
        .get(header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.split(';').next())
        .is_some_and(|t| t.trim().eq_ignore_ascii_case("application/json"));
    if ok {
        Ok(())
    } else {
        Err(ApiError::unsupported_media_type())
    }
}

/// Checks the content type, then reads the body: more than `MAX_BODY_BYTES` is 413, a body
/// that takes longer than the body timeout is 503 `timeout`.
async fn read_body(state: &AppState, headers: &HeaderMap, body: Body) -> Result<Bytes, ApiError> {
    require_json(headers)?;
    let read = axum::body::to_bytes(body, MAX_BODY_BYTES);
    match tokio::time::timeout(state.inner.limits.body_timeout, read).await {
        Err(_) => Err(ApiError::timeout()),
        Ok(r) => r.map_err(|e| {
            let too_large = std::error::Error::source(&e)
                .is_some_and(|s| s.is::<http_body_util::LengthLimitError>());
            if too_large {
                ApiError::payload_too_large()
            } else {
                ApiError::bad_request()
            }
        }),
    }
}

fn parse<T: for<'de> Deserialize<'de>>(body: &Bytes) -> Result<T, ApiError> {
    serde_json::from_slice(body).map_err(|_| ApiError::bad_request())
}

fn install_prefix(install_id: &str) -> &str {
    &install_id[..install_id.len().min(8)]
}

fn log(action: &str, outcome: &str, ip: IpAddr, install: Option<&str>, hint: Option<&str>) {
    let mut line = format!("{action} {outcome} ip={ip}");
    if let Some(i) = install {
        line.push_str(&format!(" install={}", install_prefix(i)));
    }
    if let Some(h) = hint {
        line.push_str(&format!(" serial={h}"));
    }
    println!("{line}");
}

/// Trims, drops control characters and caps the length of a free-text field.
fn clean(s: Option<String>, max: usize) -> Option<String> {
    s.map(|s| {
        s.chars()
            .filter(|c| !c.is_control())
            .take(max)
            .collect::<String>()
            .trim()
            .to_string()
    })
}

struct Signed {
    install_key: String,
    install_id: String,
}

/// Verifies the signed part of a request; `serial` is the normalised serial or "".
fn verify_signed(
    action: &str,
    install_key: Option<String>,
    timestamp: Option<i64>,
    signature: Option<String>,
    serial: &str,
    now: i64,
) -> Result<Signed, ApiError> {
    let (Some(install_key), Some(timestamp), Some(signature)) = (install_key, timestamp, signature)
    else {
        return Err(ApiError::bad_request());
    };
    let vk: VerifyingKey =
        crypto::parse_install_key(&install_key).ok_or_else(ApiError::bad_request)?;
    crypto::verify_request(
        &vk,
        action,
        &install_key,
        timestamp,
        serial,
        &signature,
        now,
    )
    .map_err(|e| match e {
        RequestAuthError::StaleTimestamp => ApiError::stale_timestamp(),
        RequestAuthError::BadSignature => ApiError::bad_signature(),
    })?;
    Ok(Signed {
        install_id: crypto::install_id(&vk),
        install_key,
    })
}

fn activation_list(acts: &[db::Activation]) -> Value {
    Value::Array(
        acts.iter()
            .map(|a| json!({ "machine": a.machine, "activated_at": a.activated_at, "last_seen_at": a.last_seen_at }))
            .collect(),
    )
}

// ---------- handlers ----------

#[derive(Deserialize)]
struct ActivateReq {
    serial: Option<String>,
    install_key: Option<String>,
    timestamp: Option<i64>,
    signature: Option<String>,
    machine: Option<String>,
    app_version: Option<String>,
}

async fn activate(
    State(state): State<AppState>,
    Extension(ClientIp(ip)): Extension<ClientIp>,
    headers: HeaderMap,
    body: Body,
) -> Result<Json<Value>, ApiError> {
    begin(&state, ip, "activate", Class::Activate)?;
    let res = activate_inner(&state, &headers, body).await;
    respond(&state, ip, "activate", res).await
}

/// `(value, install id, serial hint)` on success; the error plus whatever was known.
type Logged<T> = Result<(T, String, String), (ApiError, Option<String>, Option<String>)>;

/// The end of every signed action: log the outcome, record it for abuse tracking, and turn the
/// result into the response.
async fn respond(
    state: &AppState,
    ip: IpAddr,
    action: &'static str,
    res: Logged<Value>,
) -> Result<Json<Value>, ApiError> {
    match &res {
        Ok((_, install, hint)) => log(
            action,
            "ok",
            ip,
            Some(install),
            (!hint.is_empty()).then_some(hint.as_str()),
        ),
        Err((e, install, hint)) => log(action, e.code, ip, install.as_deref(), hint.as_deref()),
    }
    state
        .finish(
            ip,
            action,
            res.as_ref().map(|_| ()).map_err(|(e, _, _)| e.code),
        )
        .await;
    res.map(|(v, _, _)| Json(v)).map_err(|(e, _, _)| e)
}

async fn activate_inner(state: &AppState, headers: &HeaderMap, body: Body) -> Logged<Value> {
    let body = read_body(state, headers, body)
        .await
        .map_err(|e| (e, None, None))?;
    let req: ActivateReq = parse(&body).map_err(|e| (e, None, None))?;
    let Some(raw_serial) = req.serial.as_deref() else {
        return Err((ApiError::bad_request(), None, None));
    };
    if req
        .install_key
        .as_deref()
        .and_then(crypto::parse_install_key)
        .is_none()
    {
        return Err((ApiError::bad_request(), None, None));
    }
    let norm = serial::normalise(raw_serial).ok_or((ApiError::invalid_serial(), None, None))?;
    let hint = serial::hint(&norm);
    let now = state.now();
    let signed = verify_signed(
        "activate",
        req.install_key,
        req.timestamp,
        req.signature,
        &norm,
        now,
    )
    .map_err(|e| (e, None, Some(hint.clone())))?;
    let machine = clean(req.machine, MAX_MACHINE_CHARS);
    let app_version = clean(req.app_version, MAX_VERSION_CHARS);
    let hash = serial::hash(&norm);
    let install_id = signed.install_id.clone();
    let hint2 = hint.clone();

    let out = state
        .with_db(move |conn, key| -> Result<Value, ApiError> {
            let tx = conn.transaction_with_behavior(rusqlite::TransactionBehavior::Immediate)?;
            let s = db::serial_by_hash(&tx, &hash)?.ok_or_else(ApiError::unknown_serial)?;
            if s.status != "active" {
                return Err(ApiError::serial_revoked());
            }
            let existing = db::active_activation_for_install(&tx, &signed.install_id)?;
            let machine_for_token = match existing {
                Some(ref a) if a.serial_id == s.id => {
                    db::touch_activation(
                        &tx,
                        &a.id,
                        machine.as_deref(),
                        app_version.as_deref(),
                        now,
                    )?;
                    machine.clone().unwrap_or_else(|| a.machine.clone())
                }
                _ => {
                    let active = db::active_activations(&tx, &s.id)?;
                    if active.len() as i64 >= s.seats {
                        return Err(ApiError::seats_full(activation_list(&active)));
                    }
                    if let Some(old) = &existing {
                        db::end_activation(&tx, &old.id, now)?;
                    }
                    let m = machine.clone().unwrap_or_default();
                    db::insert_activation(
                        &tx,
                        &s.id,
                        &signed.install_id,
                        &signed.install_key,
                        &m,
                        app_version.as_deref().unwrap_or(""),
                        now,
                    )?;
                    m
                }
            };
            let seats_used = db::active_activations(&tx, &s.id)?.len();
            tx.commit()?;
            let payload = TokenPayload::new(
                &s.id,
                &signed.install_id,
                &s.plan,
                &machine_for_token,
                s.relay_until,
                now,
            );
            Ok(json!({
                "token": crypto::sign_token(key, &payload),
                "expires_at": payload.expires_at,
                "serial_hint": s.hint,
                "seats": s.seats,
                "seats_used": seats_used,
            }))
        })
        .await
        .and_then(|r| r);
    match out {
        Ok(v) => Ok((v, install_id, hint2)),
        Err(e) => Err((e, Some(install_id), Some(hint2))),
    }
}

#[derive(Deserialize)]
struct RenewReq {
    install_key: Option<String>,
    timestamp: Option<i64>,
    signature: Option<String>,
    app_version: Option<String>,
}

async fn renew(
    State(state): State<AppState>,
    Extension(ClientIp(ip)): Extension<ClientIp>,
    headers: HeaderMap,
    body: Body,
) -> Result<Json<Value>, ApiError> {
    begin(&state, ip, "renew", Class::Other)?;
    let res = renew_inner(&state, &headers, body).await;
    respond(&state, ip, "renew", res).await
}

async fn renew_inner(state: &AppState, headers: &HeaderMap, body: Body) -> Logged<Value> {
    let body = read_body(state, headers, body)
        .await
        .map_err(|e| (e, None, None))?;
    let req: RenewReq = parse(&body).map_err(|e| (e, None, None))?;
    let now = state.now();
    let signed = verify_signed(
        "renew",
        req.install_key,
        req.timestamp,
        req.signature,
        "",
        now,
    )
    .map_err(|e| (e, None, None))?;
    let app_version = clean(req.app_version, MAX_VERSION_CHARS);
    let install_id = signed.install_id.clone();
    let out = state
        .with_db(move |conn, key| -> Result<(Value, String), ApiError> {
            let tx = conn.transaction_with_behavior(rusqlite::TransactionBehavior::Immediate)?;
            let a = db::active_activation_for_install(&tx, &signed.install_id)?
                .ok_or_else(ApiError::not_activated)?;
            let s = db::serial_by_id(&tx, &a.serial_id)?.ok_or_else(ApiError::not_activated)?;
            if s.status != "active" {
                return Err(ApiError::serial_revoked());
            }
            db::touch_activation(&tx, &a.id, None, app_version.as_deref(), now)?;
            tx.commit()?;
            let payload = TokenPayload::new(
                &s.id,
                &signed.install_id,
                &s.plan,
                &a.machine,
                s.relay_until,
                now,
            );
            Ok((
                json!({
                    "token": crypto::sign_token(key, &payload),
                    "expires_at": payload.expires_at,
                    "serial_hint": s.hint,
                }),
                s.hint,
            ))
        })
        .await
        .and_then(|r| r);
    match out {
        Ok((v, hint)) => Ok((v, install_id, hint)),
        Err(e) => Err((e, Some(install_id), None)),
    }
}

#[derive(Deserialize)]
struct DeactivateReq {
    install_key: Option<String>,
    timestamp: Option<i64>,
    signature: Option<String>,
}

async fn deactivate(
    State(state): State<AppState>,
    Extension(ClientIp(ip)): Extension<ClientIp>,
    headers: HeaderMap,
    body: Body,
) -> Result<Json<Value>, ApiError> {
    begin(&state, ip, "deactivate", Class::Other)?;
    let res = deactivate_inner(&state, &headers, body).await;
    respond(&state, ip, "deactivate", res).await
}

async fn deactivate_inner(state: &AppState, headers: &HeaderMap, body: Body) -> Logged<Value> {
    let body = read_body(state, headers, body)
        .await
        .map_err(|e| (e, None, None))?;
    let req: DeactivateReq = parse(&body).map_err(|e| (e, None, None))?;
    let now = state.now();
    let signed = verify_signed(
        "deactivate",
        req.install_key,
        req.timestamp,
        req.signature,
        "",
        now,
    )
    .map_err(|e| (e, None, None))?;
    let install_id = signed.install_id.clone();
    let out = state
        .with_db(move |conn, _| -> Result<String, ApiError> {
            let tx = conn.transaction_with_behavior(rusqlite::TransactionBehavior::Immediate)?;
            let mut hint = String::new();
            if let Some(a) = db::active_activation_for_install(&tx, &signed.install_id)? {
                db::end_activation(&tx, &a.id, now)?;
                if let Some(s) = db::serial_by_id(&tx, &a.serial_id)? {
                    hint = s.hint;
                }
            }
            tx.commit()?;
            Ok(hint)
        })
        .await
        .and_then(|r| r);
    match out {
        Ok(hint) => Ok((json!({}), install_id, hint)),
        Err(e) => Err((e, Some(install_id), None)),
    }
}

async fn health() -> Json<Value> {
    Json(json!({ "ok": true, "version": env!("CARGO_PKG_VERSION") }))
}

// ---------- interest form ----------

/// The website's "I'm interested" form. `website` is a hidden honeypot field: people never see
/// it, bots fill every field.
#[derive(Deserialize)]
struct InterestReq {
    email: Option<String>,
    name: Option<String>,
    note: Option<String>,
    website: Option<String>,
}

const MAX_EMAIL_CHARS: usize = 254;
const MAX_NAME_CHARS: usize = 200;
const MAX_NOTE_CHARS: usize = 2000;
const MAX_UA_CHARS: usize = 200;

async fn interest(
    State(state): State<AppState>,
    Extension(ClientIp(ip)): Extension<ClientIp>,
    headers: HeaderMap,
    body: Body,
) -> Result<Json<Value>, ApiError> {
    begin(&state, ip, "interest", Class::Interest)?;
    let res = interest_inner(&state, ip, &headers, body).await;
    match &res {
        Ok(true) => log("interest", "ok", ip, None, None),
        Ok(false) => log("interest", "honeypot", ip, None, None),
        Err(e) => log("interest", e.code, ip, None, None),
    }
    state
        .finish(ip, "interest", res.as_ref().map(|_| ()).map_err(|e| e.code))
        .await;
    res.map(|_| Json(json!({ "ok": true, "message": "Thanks, we will be in touch." })))
}

/// `Ok(true)` when stored, `Ok(false)` when the honeypot was filled (nothing stored).
async fn interest_inner(
    state: &AppState,
    ip: IpAddr,
    headers: &HeaderMap,
    body: Body,
) -> Result<bool, ApiError> {
    let body = read_body(state, headers, body).await?;
    let req: InterestReq = parse(&body)?;
    if req.website.as_deref().is_some_and(|w| !w.trim().is_empty()) {
        return Ok(false);
    }
    let email =
        normalise_email(req.email.as_deref().unwrap_or("")).ok_or_else(ApiError::invalid_email)?;
    let name = clean(req.name, MAX_NAME_CHARS).filter(|s| !s.is_empty());
    let note = clean(req.note, MAX_NOTE_CHARS).filter(|s| !s.is_empty());
    let user_agent = clean(
        headers
            .get(header::USER_AGENT)
            .and_then(|v| v.to_str().ok())
            .map(str::to_string),
        MAX_UA_CHARS,
    )
    .filter(|s| !s.is_empty());
    let now = state.now();
    state
        .with_db(move |conn, key| -> Result<(), rusqlite::Error> {
            // Only a keyed hash of the client block is kept: enough to spot repeat
            // submitters, and not reversible without the server's key.
            let ip_hash = crypto::interest_ip_hash(key, ip);
            db::upsert_interest(
                conn,
                &db::InterestSubmission {
                    email: &email,
                    name: name.as_deref(),
                    note: note.as_deref(),
                    ip_hash: &ip_hash,
                    user_agent: user_agent.as_deref(),
                },
                now,
            )
            .map(|_| ()) // over a cap: nothing stored, the same answer
        })
        .await?
        .map_err(ApiError::from)?;
    Ok(true)
}

/// Trims and lower-cases an address and checks its shape: one `@`, a local part of plain
/// characters, a domain of at least two labels of letters, digits and hyphens. This is not a
/// deliverability check.
fn normalise_email(raw: &str) -> Option<String> {
    let e = raw.trim().to_ascii_lowercase();
    if e.is_empty()
        || e.len() > MAX_EMAIL_CHARS
        || e.chars().any(|c| c.is_whitespace() || c.is_control())
    {
        return None;
    }
    let (local, domain) = e.split_once('@')?;
    let local_ok = !local.is_empty()
        && local.len() <= 64
        && !local.starts_with('.')
        && !local.ends_with('.')
        && !local.contains("..")
        && local
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || "!#$%&'*+/=?^_`{|}~.-".contains(c));
    let labels: Vec<&str> = domain.split('.').collect();
    let domain_ok = labels.len() >= 2
        && labels.iter().all(|l| {
            !l.is_empty()
                && l.len() <= 63
                && !l.starts_with('-')
                && !l.ends_with('-')
                && l.chars().all(|c| c.is_ascii_alphanumeric() || c == '-')
        });
    (local_ok && domain_ok).then_some(e)
}

/// Lets the website's form call `/v1/interest` from the browser, and no other origin.
/// Responses to that origin expose `Retry-After`, so the page can show the wait.
async fn cors(State(state): State<AppState>, req: Request, next: Next) -> Response {
    let Some(site) = state.inner.interest_origin.clone() else {
        return next.run(req).await;
    };
    if req.uri().path() != "/v1/interest" {
        return next.run(req).await;
    }
    let from_site = req.headers().get(header::ORIGIN) == Some(&site);
    if !from_site {
        return next.run(req).await;
    }
    if req.method() == Method::OPTIONS {
        let mut resp = StatusCode::NO_CONTENT.into_response();
        let h = resp.headers_mut();
        h.insert(
            header::ACCESS_CONTROL_ALLOW_METHODS,
            HeaderValue::from_static("POST, OPTIONS"),
        );
        h.insert(
            header::ACCESS_CONTROL_ALLOW_HEADERS,
            HeaderValue::from_static("content-type"),
        );
        h.insert(
            header::ACCESS_CONTROL_MAX_AGE,
            HeaderValue::from_static("600"),
        );
        cors_headers(&mut resp, site);
        return resp;
    }
    let mut resp = next.run(req).await;
    cors_headers(&mut resp, site);
    resp
}

fn cors_headers(resp: &mut Response, site: HeaderValue) {
    let h = resp.headers_mut();
    h.insert(
        header::ACCESS_CONTROL_ALLOW_ORIGIN,
        site,
    );
    h.insert(
        header::ACCESS_CONTROL_EXPOSE_HEADERS,
        HeaderValue::from_static("Retry-After"),
    );
    h.append(header::VARY, HeaderValue::from_static("Origin"));
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn client_ip_trusts_forwarded_for_only_from_loopback() {
        let mut h = HeaderMap::new();
        h.insert("x-forwarded-for", "1.1.1.1, 198.51.100.7".parse().unwrap());
        let lo: SocketAddr = "127.0.0.1:5000".parse().unwrap();
        let remote: SocketAddr = "203.0.113.9:5000".parse().unwrap();
        assert_eq!(
            client_ip(Some(lo), &h),
            "198.51.100.7".parse::<IpAddr>().unwrap()
        );
        assert_eq!(
            client_ip(Some(remote), &h),
            "203.0.113.9".parse::<IpAddr>().unwrap()
        );
        assert_eq!(
            client_ip(Some(lo), &HeaderMap::new()),
            "127.0.0.1".parse::<IpAddr>().unwrap()
        );
        let mut bad = HeaderMap::new();
        bad.insert("x-forwarded-for", "not-an-ip".parse().unwrap());
        assert_eq!(
            client_ip(Some(lo), &bad),
            "127.0.0.1".parse::<IpAddr>().unwrap()
        );
    }

    #[test]
    fn normalise_email_checks_shape_and_lowercases() {
        assert_eq!(
            normalise_email("  Ada.Lovelace@Example.COM ").as_deref(),
            Some("ada.lovelace@example.com")
        );
        assert_eq!(
            normalise_email("a+tag@sub.example.co").as_deref(),
            Some("a+tag@sub.example.co")
        );
        for bad in [
            "",
            "no-at-sign",
            "a@b",
            "a@@example.com",
            "a b@example.com",
            ".a@example.com",
            "a..b@example.com",
            "a@-example.com",
            "a@example..com",
            "a@exa_mple.com",
            "ünï@example.com",
        ] {
            assert_eq!(normalise_email(bad), None, "{bad:?} should be refused");
        }
    }

    #[test]
    fn clean_caps_and_strips() {
        assert_eq!(
            clean(Some(" Mac\n\u{7}Book ".into()), 100).as_deref(),
            Some("MacBook")
        );
        assert_eq!(clean(Some("x".repeat(500)), 100).unwrap().len(), 100);
    }
}
