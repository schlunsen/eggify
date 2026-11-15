mod common;

use axum::body::Body;
use axum::http::{Request, StatusCode};
use axum::response::Response;
use common::*;
use serde_json::{json, Value};

const A: &str = "/v1/activate";
const R: &str = "/v1/renew";
const D: &str = "/v1/deactivate";

#[tokio::test]
async fn health() {
    let h = Harness::new();
    let (st, v) = h.call("GET", "/v1/health", None, "127.0.0.1:1", None).await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(v["ok"], true);
    assert!(v["version"].is_string());
}

#[tokio::test]
async fn activate_success_returns_valid_token() {
    let h = Harness::new();
    let (serial, serial_id) = h.serial(3);
    let c = Client::new(1);
    let t = now();
    let (st, v) = h
        .post(
            A,
            &c.activate_body(&serial, &norm(&serial), t, "Dev's MacBook Pro"),
        )
        .await;
    assert_eq!(st, StatusCode::OK, "{v}");
    assert_eq!(v["seats"], 3);
    assert_eq!(v["seats_used"], 1);
    assert_eq!(v["serial_hint"], format!("EGG-…-{}", &serial[22..]));
    let p = decode_token(&h.server_key, v["token"].as_str().unwrap());
    assert_eq!(p["v"], 1);
    assert_eq!(p["serial_id"], serial_id);
    assert_eq!(p["install"], c.install_id());
    assert_eq!(p["plan"], "standard");
    assert_eq!(p["features"], json!(["app", "relay"]));
    assert_eq!(p["machine"], "Dev's MacBook Pro");
    assert_eq!(p["issued_at"], t);
    assert_eq!(p["expires_at"], t + 30 * 86400);
    assert_eq!(v["expires_at"], p["expires_at"]);
}

#[tokio::test]
async fn activate_accepts_sloppy_serial_input() {
    let h = Harness::new();
    let (serial, _) = h.serial(3);
    let c = Client::new(1);
    let typed = format!("egg {}", serial[4..].to_lowercase().replace('-', " "));
    let (st, v) = h
        .post(A, &c.activate_body(&typed, &norm(&serial), now(), "m"))
        .await;
    assert_eq!(st, StatusCode::OK, "{v}");
}

#[tokio::test]
async fn reactivate_same_serial_is_idempotent() {
    let h = Harness::new();
    let (serial, _) = h.serial(3);
    let c = Client::new(1);
    let (st, _) = h
        .post(A, &c.activate_body(&serial, &norm(&serial), now(), "m"))
        .await;
    assert_eq!(st, StatusCode::OK);
    let (st, v) = h
        .post(A, &c.activate_body(&serial, &norm(&serial), now(), "m2"))
        .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(v["seats_used"], 1);
    let p = decode_token(&h.server_key, v["token"].as_str().unwrap());
    assert_eq!(p["machine"], "m2");
}

#[tokio::test]
async fn activate_on_another_serial_moves_the_install() {
    let h = Harness::new();
    let (a, a_id) = h.serial(3);
    let (b, b_id) = h.serial(3);
    let c = Client::new(1);
    assert_eq!(
        h.post(A, &c.activate_body(&a, &norm(&a), now(), "m"))
            .await
            .0,
        StatusCode::OK
    );
    let (st, v) = h.post(A, &c.activate_body(&b, &norm(&b), now(), "m")).await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(v["seats_used"], 1);
    let conn = h.admin_db();
    assert_eq!(
        eggify_license::db::active_activations(&conn, &a_id)
            .unwrap()
            .len(),
        0
    );
    assert_eq!(
        eggify_license::db::active_activations(&conn, &b_id)
            .unwrap()
            .len(),
        1
    );
    let (st, v) = h.post(R, &c.renew_body(now())).await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(
        decode_token(&h.server_key, v["token"].as_str().unwrap())["serial_id"],
        b_id
    );
}

#[tokio::test]
async fn move_to_a_full_serial_keeps_the_old_activation() {
    let h = Harness::new();
    let (a, a_id) = h.serial(3);
    let (b, _) = h.serial(1);
    assert_eq!(
        h.post(A, &Client::new(9).activate_body(&b, &norm(&b), now(), "x"))
            .await
            .0,
        StatusCode::OK
    );
    let c = Client::new(1);
    assert_eq!(
        h.post(A, &c.activate_body(&a, &norm(&a), now(), "m"))
            .await
            .0,
        StatusCode::OK
    );
    assert_eq!(
        h.post(A, &c.activate_body(&b, &norm(&b), now(), "m"))
            .await
            .0,
        StatusCode::CONFLICT
    );
    assert_eq!(
        eggify_license::db::active_activations(&h.admin_db(), &a_id)
            .unwrap()
            .len(),
        1
    );
}

#[tokio::test]
async fn fourth_install_on_three_seats_is_seats_full() {
    let h = Harness::new();
    let (serial, _) = h.serial(3);
    for i in 1..=3u8 {
        let (st, v) = h
            .post(
                A,
                &Client::new(i).activate_body(&serial, &norm(&serial), now(), &format!("Mac {i}")),
            )
            .await;
        assert_eq!(st, StatusCode::OK);
        assert_eq!(v["seats_used"], i as i64);
    }
    let (st, v) = h
        .post(
            A,
            &Client::new(4).activate_body(&serial, &norm(&serial), now(), "Mac 4"),
        )
        .await;
    assert_eq!(st, StatusCode::CONFLICT);
    assert_eq!(v["error"], "seats_full");
    assert!(v["message"].is_string());
    let acts = v["activations"].as_array().unwrap();
    assert_eq!(acts.len(), 3);
    assert_eq!(acts[0]["machine"], "Mac 1");
    assert!(acts[0]["activated_at"].is_i64());
    assert!(acts[0]["last_seen_at"].is_i64());
}

#[tokio::test]
async fn deactivate_frees_a_seat() {
    let h = Harness::new();
    let (serial, _) = h.serial(3);
    for i in 1..=3u8 {
        assert_eq!(
            h.post(
                A,
                &Client::new(i).activate_body(&serial, &norm(&serial), now(), "m")
            )
            .await
            .0,
            StatusCode::OK
        );
    }
    let (st, v) = h.post(D, &Client::new(2).deactivate_body(now())).await;
    assert_eq!((st, v), (StatusCode::OK, json!({})));
    let (st, v) = h
        .post(
            A,
            &Client::new(4).activate_body(&serial, &norm(&serial), now(), "m"),
        )
        .await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(v["seats_used"], 3);
    // Deactivated install can no longer renew.
    assert_eq!(
        h.post(R, &Client::new(2).renew_body(now())).await.1["error"],
        "not_activated"
    );
}

#[tokio::test]
async fn deactivate_when_not_activated_is_ok() {
    let h = Harness::new();
    let (st, v) = h.post(D, &Client::new(7).deactivate_body(now())).await;
    assert_eq!((st, v), (StatusCode::OK, json!({})));
}

#[tokio::test]
async fn revoked_serial_is_403() {
    let h = Harness::new();
    let (serial, id) = h.serial(3);
    let c = Client::new(1);
    assert_eq!(
        h.post(A, &c.activate_body(&serial, &norm(&serial), now(), "m"))
            .await
            .0,
        StatusCode::OK
    );
    eggify_license::admin::cmd_serial_revoke(&h.admin_db(), &id).unwrap();
    let (st, v) = h
        .post(
            A,
            &Client::new(2).activate_body(&serial, &norm(&serial), now(), "m"),
        )
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::FORBIDDEN, Some("serial_revoked"))
    );
    let (st, v) = h.post(R, &c.renew_body(now())).await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::FORBIDDEN, Some("serial_revoked"))
    );
}

#[tokio::test]
async fn unknown_serial_is_404() {
    let h = Harness::new();
    let s = "EGG-7H3K9-Q2M4X-8R6T1-V5N0T";
    let (st, v) = h
        .post(A, &Client::new(1).activate_body(s, &norm(s), now(), "m"))
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::NOT_FOUND, Some("unknown_serial"))
    );
}

#[tokio::test]
async fn invalid_serial_is_400() {
    let h = Harness::new();
    let s = "EGG-7H3K9-Q2M4X-8R6T1-V5N0A";
    let (st, v) = h
        .post(A, &Client::new(1).activate_body(s, &norm(s), now(), "m"))
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::BAD_REQUEST, Some("invalid_serial"))
    );
}

#[tokio::test]
async fn malformed_requests_are_bad_request() {
    let h = Harness::new();
    let c = Client::new(1);
    let s = "EGG-7H3K9-Q2M4X-8R6T1-V5N0T";
    for raw in ["not json", "{}", "[]"] {
        let (st, v) = h.post_raw(A, raw).await;
        assert_eq!(
            (st, v["error"].as_str()),
            (StatusCode::BAD_REQUEST, Some("bad_request")),
            "{raw}"
        );
    }
    let mut body = c.activate_body(s, &norm(s), now(), "m");
    body["install_key"] = json!(format!("{}=", c.install_key()));
    assert_eq!(h.post(A, &body).await.1["error"], "bad_request");
    let mut body = c.activate_body(s, &norm(s), now(), "m");
    body["timestamp"] = json!("123");
    assert_eq!(h.post(A, &body).await.1["error"], "bad_request");
    let mut body = c.renew_body(now());
    body.as_object_mut().unwrap().remove("signature");
    assert_eq!(h.post(R, &body).await.1["error"], "bad_request");
}

#[tokio::test]
async fn stale_timestamp_is_401() {
    let h = Harness::new();
    let (serial, _) = h.serial(3);
    let c = Client::new(1);
    for ts in [now() - 301, now() + 301] {
        let (st, v) = h
            .post(A, &c.activate_body(&serial, &norm(&serial), ts, "m"))
            .await;
        assert_eq!(
            (st, v["error"].as_str()),
            (StatusCode::UNAUTHORIZED, Some("stale_timestamp"))
        );
    }
    let (st, v) = h.post(R, &c.renew_body(now() - 1000)).await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::UNAUTHORIZED, Some("stale_timestamp"))
    );
    // Inside the window is fine.
    assert_eq!(
        h.post(
            A,
            &c.activate_body(&serial, &norm(&serial), now() - 250, "m")
        )
        .await
        .0,
        StatusCode::OK
    );
}

#[tokio::test]
async fn bad_signature_is_401() {
    let h = Harness::new();
    let (serial, _) = h.serial(3);
    let c = Client::new(1);
    let other = Client::new(2);
    let t = now();

    // Signed by another key.
    let mut body = c.activate_body(&serial, &norm(&serial), t, "m");
    body["signature"] = json!(other.sign("activate", t, &norm(&serial)));
    assert_eq!(
        h.post(A, &body).await,
        (
            StatusCode::UNAUTHORIZED,
            json!({"error": "bad_signature", "message": "The request could not be verified."})
        )
    );

    // Signed for another action, or over the formatted (not normalised) serial.
    let mut body = c.activate_body(&serial, &norm(&serial), t, "m");
    body["signature"] = json!(c.sign("renew", t, &norm(&serial)));
    assert_eq!(h.post(A, &body).await.1["error"], "bad_signature");
    let mut body = c.activate_body(&serial, &norm(&serial), t, "m");
    body["signature"] = json!(c.sign("activate", t, &serial));
    assert_eq!(h.post(A, &body).await.1["error"], "bad_signature");

    // Renew signature reused for deactivate.
    let mut body = c.deactivate_body(t);
    body["signature"] = json!(c.sign("renew", t, ""));
    assert_eq!(h.post(D, &body).await.1["error"], "bad_signature");
    let mut body = c.renew_body(t);
    body["signature"] = json!("garbage");
    assert_eq!(h.post(R, &body).await.1["error"], "bad_signature");
}

#[tokio::test]
async fn renew_success_and_not_activated() {
    let h = Harness::new();
    let (serial, serial_id) = h.serial(3);
    let c = Client::new(1);
    let (st, v) = h.post(R, &c.renew_body(now())).await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::NOT_FOUND, Some("not_activated"))
    );
    assert_eq!(
        h.post(A, &c.activate_body(&serial, &norm(&serial), now(), "Mac"))
            .await
            .0,
        StatusCode::OK
    );
    let (st, v) = h.post(R, &c.renew_body(now())).await;
    assert_eq!(st, StatusCode::OK, "{v}");
    assert!(v["serial_hint"].as_str().unwrap().starts_with("EGG-…-"));
    let p = decode_token(&h.server_key, v["token"].as_str().unwrap());
    assert_eq!(p["serial_id"], serial_id);
    assert_eq!(p["install"], c.install_id());
    assert_eq!(p["machine"], "Mac");
    assert_eq!(v["expires_at"], p["expires_at"]);
    let acts = eggify_license::db::active_activations(&h.admin_db(), &serial_id).unwrap();
    assert_eq!(acts[0].app_version, "2.18.1");
}

#[tokio::test]
async fn activate_rate_limit_is_10_per_minute_per_ip() {
    let h = Harness::new();
    let (serial, _) = h.serial(3);
    let c = Client::new(1);
    // Successful (idempotent) activations, so the failure ban doesn't interfere.
    for _ in 0..10 {
        let body = c.activate_body(&serial, &norm(&serial), now(), "m");
        assert_eq!(
            h.call("POST", A, Some(&body), "198.51.100.1:1", None)
                .await
                .0,
            StatusCode::OK
        );
    }
    let body = c.activate_body(&serial, &norm(&serial), now(), "m");
    let (st, retry, v) = h
        .call_full("POST", A, Some(&body), "198.51.100.1:1", None)
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::TOO_MANY_REQUESTS, Some("rate_limited"))
    );
    assert_eq!(retry, Some(60));
    // Another IP and another class are unaffected.
    assert_eq!(
        h.call("POST", A, Some(&body), "198.51.100.2:1", None)
            .await
            .0,
        StatusCode::OK
    );
    assert_eq!(
        h.call(
            "POST",
            R,
            Some(&c.renew_body(now())),
            "198.51.100.1:1",
            None
        )
        .await
        .0,
        StatusCode::OK
    );
}

#[tokio::test]
async fn forwarded_for_is_trusted_only_from_loopback() {
    let h = Harness::new();
    let c = Client::new(1);
    let body = c.renew_body(now());
    // Behind Caddy (loopback peer): the forwarded client is limited, not the proxy.
    for _ in 0..60 {
        assert_eq!(
            h.call("POST", R, Some(&body), "127.0.0.1:5", Some("192.0.2.1"))
                .await
                .0,
            StatusCode::NOT_FOUND
        );
    }
    assert_eq!(
        h.call("POST", R, Some(&body), "127.0.0.1:5", Some("192.0.2.1"))
            .await
            .0,
        StatusCode::TOO_MANY_REQUESTS
    );
    assert_eq!(
        h.call("POST", R, Some(&body), "127.0.0.1:5", Some("192.0.2.2"))
            .await
            .0,
        StatusCode::NOT_FOUND
    );
    // A remote peer can't dodge its limit with a forged header.
    for i in 0..60 {
        let xff = format!("192.0.2.{}", 100 + i);
        assert_eq!(
            h.call("POST", R, Some(&body), "198.51.100.9:5", Some(&xff))
                .await
                .0,
            StatusCode::NOT_FOUND
        );
    }
    assert_eq!(
        h.call(
            "POST",
            R,
            Some(&body),
            "198.51.100.9:5",
            Some("192.0.2.250")
        )
        .await
        .0,
        StatusCode::TOO_MANY_REQUESTS
    );
}

#[tokio::test]
async fn unknown_route_is_json_404() {
    let h = Harness::new();
    let (st, v) = h
        .call("GET", "/license-api/v1/health", None, "127.0.0.1:1", None)
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::NOT_FOUND, Some("not_found"))
    );
}

#[tokio::test]
async fn relay_feature_follows_the_subscription() {
    let h = Harness::with_clock(now());
    let (serial, id) = h.serial_relay(3, 0);
    let c = Client::new(1);
    // No relay: app only, relay_until null.
    let (st, v) = h
        .post(A, &c.activate_body(&serial, &norm(&serial), h.time(), "m"))
        .await;
    assert_eq!(st, StatusCode::OK);
    let p = decode_token(&h.server_key, v["token"].as_str().unwrap());
    assert_eq!(p["features"], json!(["app"]));
    assert!(p["relay_until"].is_null());

    // Extend by 12 months: the next renewal carries relay.
    eggify_license::admin::set_relay(
        &h.admin_db(),
        &id,
        eggify_license::admin::RelayChange::ExtendMonths(12),
    )
    .unwrap();
    let until = eggify_license::db::serial_by_id(&h.admin_db(), &id)
        .unwrap()
        .unwrap()
        .relay_until
        .unwrap();
    let (_, v) = h.post(R, &c.renew_body(h.time())).await;
    let p = decode_token(&h.server_key, v["token"].as_str().unwrap());
    assert_eq!(p["features"], json!(["app", "relay"]));
    assert_eq!(p["relay_until"], until);
    assert_eq!(
        p["expires_at"],
        h.time() + 30 * 86400,
        "token lifetime unchanged"
    );

    // After the subscription ends: app only, relay_until still reported.
    h.set_time(until);
    let (_, v) = h.post(R, &c.renew_body(h.time())).await;
    let p = decode_token(&h.server_key, v["token"].as_str().unwrap());
    assert_eq!(p["features"], json!(["app"]));
    assert_eq!(p["relay_until"], until);
    h.set_time(until - 1);
    let (_, v) = h.post(R, &c.renew_body(h.time())).await;
    assert_eq!(
        decode_token(&h.server_key, v["token"].as_str().unwrap())["features"],
        json!(["app", "relay"])
    );

    // --until and --off.
    let d = eggify_license::admin::parse_until_date("2099-12-31").unwrap();
    eggify_license::admin::set_relay(
        &h.admin_db(),
        &serial,
        eggify_license::admin::RelayChange::Until(d),
    )
    .unwrap();
    let (_, v) = h.post(R, &c.renew_body(h.time())).await;
    assert_eq!(
        decode_token(&h.server_key, v["token"].as_str().unwrap())["relay_until"],
        d
    );
    eggify_license::admin::set_relay(
        &h.admin_db(),
        &serial,
        eggify_license::admin::RelayChange::Off,
    )
    .unwrap();
    let (_, v) = h
        .post(A, &c.activate_body(&serial, &norm(&serial), h.time(), "m"))
        .await;
    let p = decode_token(&h.server_key, v["token"].as_str().unwrap());
    assert_eq!(p["features"], json!(["app"]));
    assert!(p["relay_until"].is_null());
}

// ---------- the "I'm interested" form ----------

const I: &str = "/v1/interest";
const SITE: &str = "https://example.com";

/// (email, name, note, submissions) for every sign-up, by address.
fn interest_rows(h: &Harness) -> Vec<(String, Option<String>, Option<String>, i64)> {
    let db = h.admin_db();
    let mut st = db
        .prepare("SELECT email, name, note, submissions FROM interests ORDER BY email")
        .unwrap();
    let rows = st
        .query_map([], |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?)))
        .unwrap();
    rows.map(|r| r.unwrap()).collect()
}

fn request(method: &str, path: &str, origin: Option<&str>, body: Option<&Value>) -> Request<Body> {
    let mut b = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json");
    if let Some(o) = origin {
        b = b.header("origin", o);
    }
    if method == "OPTIONS" {
        b = b
            .header("access-control-request-method", "POST")
            .header("access-control-request-headers", "content-type");
    }
    let body = match body {
        Some(v) => Body::from(v.to_string()),
        None => Body::empty(),
    };
    b.body(body).unwrap()
}

fn hdr(resp: &Response, name: &str) -> Option<String> {
    resp.headers()
        .get(name)
        .map(|v| v.to_str().unwrap().to_string())
}

#[tokio::test]
async fn interest_stores_a_signup_and_merges_repeats() {
    let h = Harness::new();
    let (st, v) = h
        .post(
            I,
            &json!({ "email": " Ada@Example.com ", "name": "Ada", "note": "Keen" }),
        )
        .await;
    assert_eq!((st, v["ok"].as_bool()), (StatusCode::OK, Some(true)));
    // The same address in another case, without a name: one row, name and note kept.
    let (st, _) = h.post(I, &json!({ "email": "ADA@example.com" })).await;
    assert_eq!(st, StatusCode::OK);
    assert_eq!(
        interest_rows(&h),
        vec![(
            "ada@example.com".to_string(),
            Some("Ada".to_string()),
            Some("Keen".to_string()),
            2
        )]
    );
}

#[tokio::test]
async fn interest_honeypot_answers_ok_and_stores_nothing() {
    let h = Harness::new();
    let (st, v) = h
        .post(
            I,
            &json!({ "email": "bot@example.com", "website": "http://spam.example" }),
        )
        .await;
    assert_eq!((st, v["ok"].as_bool()), (StatusCode::OK, Some(true)));
    // A filled honeypot is answered as success even without a usable address.
    let (st, v) = h.post(I, &json!({ "website": "x" })).await;
    assert_eq!((st, v["ok"].as_bool()), (StatusCode::OK, Some(true)));
    assert!(interest_rows(&h).is_empty());
}

#[tokio::test]
async fn interest_refuses_bad_emails_and_bad_emails_never_ban() {
    let h = Harness::with_clock(now());
    let peer = "203.0.113.60:1000";
    // Five refusals fit the window; each says what is wrong with the address.
    for _ in 0..5 {
        let (st, _, v) = h
            .call_full(
                "POST",
                I,
                Some(&json!({ "email": "not-an-email" })),
                peer,
                None,
            )
            .await;
        assert_eq!(
            (st, v["error"].as_str()),
            (StatusCode::BAD_REQUEST, Some("invalid_email"))
        );
    }
    // The sixth is only the per-minute limit, never a ban (a ban says temporarily_blocked).
    let (st, retry, v) = h
        .call_full(
            "POST",
            I,
            Some(&json!({ "email": "not-an-email" })),
            peer,
            None,
        )
        .await;
    assert_eq!(
        (st, v["error"].as_str(), retry),
        (
            StatusCode::TOO_MANY_REQUESTS,
            Some("rate_limited"),
            Some(60)
        )
    );
    // Other clients are unaffected: a good address goes through.
    let (st, _, _) = h
        .call_full(
            "POST",
            I,
            Some(&json!({ "email": "ok@example.com" })),
            "203.0.113.61:1000",
            None,
        )
        .await;
    assert_eq!(st, StatusCode::OK);
}

#[tokio::test]
async fn interest_is_five_a_minute_per_client_and_not_banned() {
    let h = Harness::with_clock(now());
    let peer = "198.51.100.77:1";
    for i in 0..5 {
        let (st, _, _) = h
            .call_full(
                "POST",
                I,
                Some(&json!({ "email": format!("p{i}@example.com") })),
                peer,
                None,
            )
            .await;
        assert_eq!(st, StatusCode::OK);
    }
    let (st, retry, v) = h
        .call_full(
            "POST",
            I,
            Some(&json!({ "email": "p5@example.com" })),
            peer,
            None,
        )
        .await;
    assert_eq!(
        (st, v["error"].as_str(), retry),
        (
            StatusCode::TOO_MANY_REQUESTS,
            Some("rate_limited"),
            Some(60)
        )
    );
    // Still over the limit, still only rate_limited: no failure ban for a form.
    let (st, _, v) = h
        .call_full(
            "POST",
            I,
            Some(&json!({ "email": "p6@example.com" })),
            peer,
            None,
        )
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::TOO_MANY_REQUESTS, Some("rate_limited"))
    );
    // Another client is not affected.
    let (st, _, _) = h
        .call_full(
            "POST",
            I,
            Some(&json!({ "email": "p7@example.com" })),
            "198.51.100.78:1",
            None,
        )
        .await;
    assert_eq!(st, StatusCode::OK);
}

#[tokio::test]
async fn interest_cors_exposes_retry_after_to_the_site() {
    let h = Harness::new();
    let peer = "198.51.100.88:1";
    // Preflight from the site: answered before routing, with the expose list.
    let resp = h.send(request("OPTIONS", I, Some(SITE), None), peer).await;
    assert_eq!(resp.status(), StatusCode::NO_CONTENT);
    assert_eq!(
        hdr(&resp, "access-control-allow-origin").as_deref(),
        Some(SITE)
    );
    assert_eq!(
        hdr(&resp, "access-control-allow-methods").as_deref(),
        Some("POST, OPTIONS")
    );
    assert_eq!(
        hdr(&resp, "access-control-expose-headers").as_deref(),
        Some("Retry-After")
    );
    // Five submissions from the site, then the sixth is refused with the wait the page shows.
    for i in 0..5 {
        let body = json!({ "email": format!("c{i}@example.com") });
        let resp = h
            .send(request("POST", I, Some(SITE), Some(&body)), peer)
            .await;
        assert_eq!(resp.status(), StatusCode::OK);
    }
    let body = json!({ "email": "c5@example.com" });
    let resp = h
        .send(request("POST", I, Some(SITE), Some(&body)), peer)
        .await;
    assert_eq!(resp.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(hdr(&resp, "retry-after").as_deref(), Some("60"));
    assert_eq!(
        hdr(&resp, "access-control-expose-headers").as_deref(),
        Some("Retry-After")
    );
    assert_eq!(
        hdr(&resp, "access-control-allow-origin").as_deref(),
        Some(SITE)
    );
    let v = json_of(resp).await;
    assert_eq!(v["error"].as_str(), Some("rate_limited"));
}

#[tokio::test]
async fn interest_cors_is_for_the_site_only() {
    let h = Harness::new();
    let other = "https://evil.example";
    // A POST from another origin still reaches the handler (CORS is the browser's rule), but
    // the response grants that origin nothing.
    let body = json!({ "email": "x@example.com" });
    let resp = h
        .send(
            request("POST", I, Some(other), Some(&body)),
            "203.0.113.90:1",
        )
        .await;
    assert_eq!(resp.status(), StatusCode::OK);
    assert!(hdr(&resp, "access-control-allow-origin").is_none());
    // And its preflight is not answered as one.
    let resp = h
        .send(request("OPTIONS", I, Some(other), None), "203.0.113.91:1")
        .await;
    assert_ne!(resp.status(), StatusCode::NO_CONTENT);
    assert!(hdr(&resp, "access-control-allow-origin").is_none());
}

#[tokio::test]
async fn interest_refuses_oversized_bodies() {
    let h = Harness::new();
    let big = json!({ "email": "big@example.com", "note": "x".repeat(20_000) });
    let (st, v) = h.post(I, &big).await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::PAYLOAD_TOO_LARGE, Some("payload_too_large"))
    );
}

async fn json_of(resp: Response) -> Value {
    let bytes = axum::body::to_bytes(resp.into_body(), usize::MAX)
        .await
        .unwrap();
    serde_json::from_slice(&bytes).unwrap_or(Value::Null)
}

// ---------- content type ----------

#[tokio::test]
async fn post_endpoints_require_application_json() {
    let h = Harness::with_clock(now());
    let peer = "203.0.113.70:1";
    for path in [A, R, D, I] {
        for ct in [
            Some("text/plain"),
            Some("application/x-www-form-urlencoded"),
            None,
        ] {
            let mut b = Request::builder().method("POST").uri(path);
            if let Some(ct) = ct {
                b = b.header("content-type", ct);
            }
            let body = json!({ "email": "forged@example.com" }).to_string();
            let resp = h.send(b.body(Body::from(body)).unwrap(), peer).await;
            assert_eq!(
                resp.status(),
                StatusCode::UNSUPPORTED_MEDIA_TYPE,
                "{path} {ct:?}"
            );
        }
    }
    assert!(interest_rows(&h).is_empty());
    // Not a ban failure, so the client still works afterwards; charset parameters are fine.
    let req = Request::builder()
        .method("POST")
        .uri(I)
        .header("content-type", "Application/JSON; charset=utf-8")
        .body(Body::from(json!({ "email": "ok@example.com" }).to_string()))
        .unwrap();
    assert_eq!(h.send(req, peer).await.status(), StatusCode::OK);
    assert_eq!(interest_rows(&h).len(), 1);
}

#[tokio::test]
async fn interest_ip_is_stored_as_a_keyed_hash() {
    use sha2::{Digest, Sha256};
    let h = Harness::new();
    let ip = "203.0.113.9";
    h.call(
        "POST",
        I,
        Some(&json!({ "email": "a@example.com" })),
        &format!("{ip}:1"),
        None,
    )
    .await;
    let stored: String = h
        .admin_db()
        .query_row("SELECT ip_hash FROM interests", [], |r| r.get(0))
        .unwrap();
    assert_eq!(stored.len(), 64);
    assert_ne!(stored, hex::encode(Sha256::digest(ip.as_bytes())));
}
