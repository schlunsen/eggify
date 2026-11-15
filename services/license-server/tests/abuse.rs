mod common;

use axum::body::{Body, Bytes};
use axum::http::{Request, StatusCode};
use common::*;
use eggify_license::abuse::BAN_MAX_SECS;
use eggify_license::api::Limits;
use http_body_util::BodyExt;
use std::pin::Pin;
use std::task::{Context, Poll};
use std::time::Duration;

const A: &str = "/v1/activate";
const R: &str = "/v1/renew";
const UNKNOWN: &str = "EGG-7H3K9-Q2M4X-8R6T1-V5N0T";

/// One failed activation (unknown serial) from `peer`, signed at the server's time.
async fn fail(h: &Harness, peer: &str) -> StatusCode {
    let c = Client::new(1);
    h.call(
        "POST",
        A,
        Some(&c.activate_body(UNKNOWN, &norm(UNKNOWN), h.time(), "m")),
        peer,
        None,
    )
    .await
    .0
}

/// One failed renew (bad signature) from `peer`.
async fn fail_renew(h: &Harness, peer: &str) -> StatusCode {
    let mut body = Client::new(1).renew_body(h.time());
    body["signature"] = serde_json::json!(Client::new(2).sign("renew", h.time(), ""));
    h.call("POST", R, Some(&body), peer, None).await.0
}

#[tokio::test]
async fn five_failures_ban_the_client_for_an_hour() {
    let h = Harness::with_clock(now());
    let (serial, _) = h.serial(3);
    let bad = "198.51.100.20:1";
    for _ in 0..4 {
        assert_eq!(fail(&h, bad).await, StatusCode::NOT_FOUND);
    }
    // The 5th failure is answered normally, then the ban starts.
    assert_eq!(fail(&h, bad).await, StatusCode::NOT_FOUND);

    let good = Client::new(5).activate_body(&serial, &norm(&serial), h.time(), "m");
    let (st, retry, v) = h.call_full("POST", A, Some(&good), bad, None).await;
    assert_eq!(st, StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(v["error"], "temporarily_blocked");
    assert!(
        v["message"].as_str().unwrap().contains("about 60 minutes"),
        "{v}"
    );
    assert_eq!(v.as_object().unwrap().len(), 2, "spec error format: {v}");
    assert_eq!(retry, Some(3600));
    // Every route but health; the IPv6 /64 rule doesn't matter for v4; other clients are fine.
    assert_eq!(
        h.call(
            "POST",
            R,
            Some(&Client::new(5).renew_body(h.time())),
            bad,
            None
        )
        .await
        .1["error"],
        "temporarily_blocked"
    );
    assert_eq!(
        h.call("GET", "/nope", None, bad, None).await.1["error"],
        "temporarily_blocked"
    );
    let (st, v) = h.call("GET", "/v1/health", None, bad, None).await;
    assert_eq!((st, v["ok"].as_bool()), (StatusCode::OK, Some(true)));
    assert_eq!(
        h.call("POST", A, Some(&good), "198.51.100.21:1", None)
            .await
            .0,
        StatusCode::OK
    );

    // Ends after the hour.
    h.set_time(h.time() + 3599);
    assert_eq!(
        h.call_full("POST", A, Some(&good), bad, None).await.1,
        Some(1)
    );
    h.set_time(h.time() + 1);
    let good = Client::new(5).activate_body(&serial, &norm(&serial), h.time(), "m");
    assert_eq!(
        h.call("POST", A, Some(&good), bad, None).await.0,
        StatusCode::OK
    );
}

#[tokio::test]
async fn behind_caddy_the_forwarded_client_is_banned_and_ipv6_per_64() {
    let h = Harness::with_clock(now());
    let c = Client::new(1);
    let body = c.activate_body(UNKNOWN, &norm(UNKNOWN), h.time(), "m");
    for i in 0..5 {
        let xff = format!("2001:db8:aa:bb::{i}");
        h.call("POST", A, Some(&body), "127.0.0.1:9", Some(&xff))
            .await;
    }
    let (st, v) = h
        .call(
            "POST",
            A,
            Some(&body),
            "127.0.0.1:9",
            Some("2001:db8:aa:bb:ffff::1"),
        )
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::TOO_MANY_REQUESTS, Some("temporarily_blocked"))
    );
    // Neighbouring /64 and the proxy's other clients are not.
    assert_eq!(
        h.call(
            "POST",
            A,
            Some(&body),
            "127.0.0.1:9",
            Some("2001:db8:aa:bc::1")
        )
        .await
        .0,
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        h.call("POST", A, Some(&body), "127.0.0.1:9", Some("192.0.2.77"))
            .await
            .0,
        StatusCode::NOT_FOUND
    );
}

#[tokio::test]
async fn success_resets_the_failure_window_not_the_ban() {
    let h = Harness::with_clock(now());
    let (serial, _) = h.serial(3);
    let ip = "198.51.100.30:1";
    let ok = |h: &Harness| Client::new(6).activate_body(&serial, &norm(&serial), h.time(), "m");
    for _ in 0..4 {
        fail_renew(&h, ip).await;
    }
    assert_eq!(
        h.call("POST", A, Some(&ok(&h)), ip, None).await.0,
        StatusCode::OK
    );
    for _ in 0..4 {
        assert_eq!(fail_renew(&h, ip).await, StatusCode::UNAUTHORIZED);
    }
    assert_eq!(
        h.call("POST", A, Some(&ok(&h)), ip, None).await.0,
        StatusCode::OK
    );
    // Without a success in between, five failures ban.
    for _ in 0..5 {
        fail_renew(&h, ip).await;
    }
    assert_eq!(
        h.call("POST", A, Some(&ok(&h)), ip, None).await.0,
        StatusCode::TOO_MANY_REQUESTS
    );
}

#[tokio::test]
async fn bans_escalate_and_cap_at_seven_days() {
    let h = Harness::with_clock(now());
    let ip = "198.51.100.40:1";
    let mut seen = vec![];
    // 9 bans (Class::Other allows 60 requests a minute: 9 × 6 = 54).
    for _ in 0..9 {
        for _ in 0..5 {
            assert_eq!(fail_renew(&h, ip).await, StatusCode::UNAUTHORIZED);
        }
        let (st, retry, _) = h
            .call_full(
                "POST",
                R,
                Some(&Client::new(1).renew_body(h.time())),
                ip,
                None,
            )
            .await;
        assert_eq!(st, StatusCode::TOO_MANY_REQUESTS);
        let retry = retry.unwrap();
        seen.push(retry);
        h.set_time(h.time() + retry);
    }
    assert_eq!(
        seen,
        [
            3600,
            7200,
            14400,
            28800,
            57600,
            115200,
            230400,
            460800,
            BAN_MAX_SECS
        ]
    );
}

#[tokio::test]
async fn bans_and_their_count_survive_a_restart() {
    let mut h = Harness::with_clock(now());
    let ip = "198.51.100.50:1";
    for _ in 0..5 {
        fail(&h, ip).await;
    }
    h.restart();
    let (st, retry, _) = h.call_full("GET", "/x", None, ip, None).await;
    assert_eq!((st, retry), (StatusCode::TOO_MANY_REQUESTS, Some(3600)));
    // After it ends, the next ban (on a restarted server) is the second one: 2 h.
    h.set_time(h.time() + 3600);
    h.restart();
    for _ in 0..5 {
        assert_eq!(fail(&h, ip).await, StatusCode::NOT_FOUND);
    }
    h.restart();
    assert_eq!(h.call_full("GET", "/x", None, ip, None).await.1, Some(7200));
}

#[tokio::test]
async fn admin_bans_reach_a_running_server() {
    let h = Harness::new();
    let ip = "203.0.113.60:1";
    assert_eq!(
        h.call("GET", "/x", None, ip, None).await.0,
        StatusCode::NOT_FOUND
    );
    eggify_license::admin::cmd_ban_add(&h.admin_db(), "203.0.113.0/24", 2.0, "test").unwrap();
    h.state.maintenance().await;
    let (st, retry, v) = h.call_full("GET", "/x", None, ip, None).await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::TOO_MANY_REQUESTS, Some("temporarily_blocked"))
    );
    assert!((7198..=7200).contains(&retry.unwrap()));
    eggify_license::admin::cmd_ban_remove(&h.admin_db(), "203.0.113.0/24").unwrap();
    h.state.maintenance().await;
    assert_eq!(
        h.call("GET", "/x", None, ip, None).await.0,
        StatusCode::NOT_FOUND
    );
}

#[tokio::test]
async fn expired_bans_are_cleaned_up_after_30_days() {
    let h = Harness::with_clock(now());
    let ip = "198.51.100.70:1";
    for _ in 0..5 {
        fail(&h, ip).await;
    }
    h.state.maintenance().await;
    assert_eq!(
        eggify_license::db::load_bans(&h.admin_db()).unwrap().len(),
        1
    );
    h.set_time(h.time() + 3600 + 29 * 86400);
    h.state.maintenance().await;
    assert_eq!(
        eggify_license::db::load_bans(&h.admin_db()).unwrap().len(),
        1,
        "count still remembered"
    );
    h.set_time(h.time() + 2 * 86400);
    h.state.maintenance().await;
    assert!(eggify_license::db::load_bans(&h.admin_db())
        .unwrap()
        .is_empty());
}

#[tokio::test]
async fn global_brake_on_failed_activations_and_recovery() {
    let h = Harness::with_clock(now());
    let (serial, _) = h.serial(3);
    let t0 = h.time();
    // 100 failures from 100 clients: not yet.
    for i in 0..100u32 {
        assert_eq!(
            fail(&h, &format!("10.9.{}.{}:1", i / 200, i % 200 + 1)).await,
            StatusCode::NOT_FOUND
        );
    }
    let ok =
        |h: &Harness, n: u8| Client::new(n).activate_body(&serial, &norm(&serial), h.time(), "m");
    assert_eq!(
        h.call("POST", A, Some(&ok(&h, 10)), &fresh_peer(), None)
            .await
            .0,
        StatusCode::OK
    );
    // The 101st trips it.
    h.set_time(t0 + 600);
    assert_eq!(fail(&h, "10.9.9.9:1").await, StatusCode::NOT_FOUND);
    let (st, retry, v) = h
        .call_full("POST", A, Some(&ok(&h, 11)), &fresh_peer(), None)
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::SERVICE_UNAVAILABLE, Some("busy"))
    );
    assert_eq!(
        v["message"],
        "Too many failed activations right now; try again later."
    );
    assert_eq!(retry, Some(3000)); // the oldest failure (t0) ages out at t0 + 3600
                                   // Renew, deactivate and health don't care.
    let renew = Client::new(10).renew_body(h.time());
    assert_eq!(
        h.call("POST", R, Some(&renew), &fresh_peer(), None).await.0,
        StatusCode::OK
    );
    assert_eq!(
        h.call("GET", "/v1/health", None, &fresh_peer(), None)
            .await
            .0,
        StatusCode::OK
    );
    // Clears once the hour's count drops back to 100.
    h.set_time(t0 + 3599);
    assert_eq!(
        h.call("POST", A, Some(&ok(&h, 11)), &fresh_peer(), None)
            .await
            .0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    h.set_time(t0 + 3600);
    assert_eq!(
        h.call("POST", A, Some(&ok(&h, 11)), &fresh_peer(), None)
            .await
            .0,
        StatusCode::OK
    );
}

#[tokio::test]
async fn body_over_16kb_is_413() {
    let h = Harness::new();
    let big = format!("{{\"serial\":\"{}\"}}", "A".repeat(17 * 1024));
    let (st, v) = h.post_raw(A, &big).await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::PAYLOAD_TOO_LARGE, Some("payload_too_large"))
    );
    assert!(v["message"].is_string());
    // Just under the limit is parsed (and refused as a bad request).
    let ok_size = format!("{{\"x\":\"{}\"}}", "A".repeat(16 * 1024 - 20));
    assert_eq!(h.post_raw(A, &ok_size).await.1["error"], "bad_request");
}

/// A request body that never arrives.
struct Stalled;

impl http_body::Body for Stalled {
    type Data = Bytes;
    type Error = std::convert::Infallible;
    fn poll_frame(
        self: Pin<&mut Self>,
        _: &mut Context<'_>,
    ) -> Poll<Option<Result<http_body::Frame<Bytes>, Self::Error>>> {
        Poll::Pending
    }
}

fn stalled_request() -> Request<Body> {
    Request::builder()
        .method("POST")
        .uri(R)
        .header("content-type", "application/json")
        .body(Body::new(Stalled))
        .unwrap()
}

#[tokio::test]
async fn slow_requests_time_out_and_in_flight_is_capped() {
    let h = Harness::with_limits(Limits {
        timeout: Duration::from_millis(300),
        max_in_flight: 1,
        ..Limits::default()
    });
    let app_h = std::sync::Arc::new(h);
    let h2 = app_h.clone();
    let slow = tokio::spawn(async move { h2.send(stalled_request(), "198.51.100.80:1").await });
    tokio::time::sleep(Duration::from_millis(50)).await;
    // The one slot is taken.
    let (st, retry, v) = app_h
        .call_full("GET", "/v1/health", None, "198.51.100.81:1", None)
        .await;
    assert_eq!(
        (st, v["error"].as_str(), retry),
        (StatusCode::SERVICE_UNAVAILABLE, Some("busy"), Some(5))
    );
    // The stalled one is cut off with a JSON error.
    let resp = slow.await.unwrap();
    assert_eq!(resp.status(), StatusCode::SERVICE_UNAVAILABLE);
    let v: serde_json::Value =
        serde_json::from_slice(&resp.into_body().collect().await.unwrap().to_bytes()).unwrap();
    assert_eq!(v["error"], "timeout");
    // And the slot is free again.
    assert_eq!(
        app_h
            .call("GET", "/v1/health", None, "198.51.100.81:1", None)
            .await
            .0,
        StatusCode::OK
    );
}

async fn deactivate_ok(h: &Harness, peer: &str) -> StatusCode {
    let c = Client::new(9);
    h.call(
        "POST",
        "/v1/deactivate",
        Some(&c.deactivate_body(h.time())),
        peer,
        None,
    )
    .await
    .0
}

#[tokio::test]
async fn deactivate_and_interest_successes_do_not_clear_the_failure_window() {
    let h = Harness::with_clock(now());
    let peer = "198.51.100.90:1";
    for _ in 0..4 {
        assert_eq!(fail(&h, peer).await, StatusCode::NOT_FOUND);
    }
    assert_eq!(deactivate_ok(&h, peer).await, StatusCode::OK);
    let (st, _) = h
        .call(
            "POST",
            "/v1/interest",
            Some(&serde_json::json!({"email": "a@example.com"})),
            peer,
            None,
        )
        .await;
    assert_eq!(st, StatusCode::OK);
    // The 5th failure still counts, so the client is banned.
    assert_eq!(fail(&h, peer).await, StatusCode::NOT_FOUND);
    assert_eq!(fail(&h, peer).await, StatusCode::TOO_MANY_REQUESTS);
}

#[tokio::test]
async fn global_brake_counts_only_unknown_serials() {
    let h = Harness::with_clock(now());
    let (serial, _) = h.serial(3);
    for i in 0..120 {
        let peer = format!("10.{}.{}.1:1", i / 200, i % 200);
        let c = Client::new(3);
        let body = c.activate_body("garbage", "garbage", h.time(), "m");
        let (st, v) = h.call("POST", A, Some(&body), &peer, None).await;
        assert_eq!(
            (st, v["error"].as_str()),
            (StatusCode::BAD_REQUEST, Some("invalid_serial"))
        );
    }
    let good = Client::new(4).activate_body(&serial, &norm(&serial), h.time(), "m");
    assert_eq!(h.post(A, &good).await.0, StatusCode::OK);
    // Unknown serials do trip it.
    for i in 0..101 {
        let peer = format!("10.9.{}.1:1", i);
        assert_eq!(fail(&h, &peer).await, StatusCode::NOT_FOUND);
    }
    let good = Client::new(5).activate_body(&serial, &norm(&serial), h.time(), "m");
    assert_eq!(h.post(A, &good).await.0, StatusCode::SERVICE_UNAVAILABLE);
}

#[tokio::test]
async fn stale_timestamp_with_a_valid_signature_never_bans() {
    let h = Harness::with_clock(now());
    let peer = "198.51.100.91:1";
    let c = Client::new(6);
    for _ in 0..10 {
        let (st, v) = h
            .call("POST", R, Some(&c.renew_body(h.time() - 1000)), peer, None)
            .await;
        assert_eq!(
            (st, v["error"].as_str()),
            (StatusCode::UNAUTHORIZED, Some("stale_timestamp"))
        );
    }
    assert_eq!(
        h.call("POST", R, Some(&c.renew_body(h.time())), peer, None)
            .await
            .0,
        StatusCode::NOT_FOUND
    );
    // A stale timestamp with a bad signature is a bad signature, and counts.
    let mut bad = c.renew_body(h.time() - 1000);
    bad["signature"] = serde_json::json!(Client::new(7).sign("renew", h.time() - 1000, ""));
    for _ in 0..5 {
        let (_, v) = h.call("POST", R, Some(&bad), peer, None).await;
        assert!(v["error"] == "bad_signature" || v["error"] == "temporarily_blocked");
    }
    assert_eq!(
        h.call("POST", R, Some(&c.renew_body(h.time())), peer, None)
            .await
            .0,
        StatusCode::TOO_MANY_REQUESTS
    );
}

#[tokio::test]
async fn escalation_reads_the_ban_row_not_the_30_second_cache() {
    use eggify_license::abuse::{Ban, Net};
    let h = Harness::with_clock(now());
    let peer = "198.51.100.92:1";
    let t = h.time();
    // An admin-side write the running server hasn't reloaded yet: 3 earlier bans, ended.
    eggify_license::db::upsert_ban(
        &h.admin_db(),
        &Ban {
            net: Net::client("198.51.100.92".parse().unwrap()),
            banned_until: t - 1,
            ban_count: 3,
            last_ban_at: t - 10,
            last_code: "unknown_serial".into(),
            source: "auto".into(),
            note: String::new(),
        },
        t,
    )
    .unwrap();
    for _ in 0..5 {
        fail(&h, peer).await;
    }
    let (st, retry, _) = h
        .call_full("POST", A, Some(&serde_json::json!({})), peer, None)
        .await;
    assert_eq!(st, StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(retry, Some(8 * 3600), "4th ban is 8 h");
}

#[tokio::test]
async fn body_read_has_its_own_short_timeout() {
    let h = Harness::with_limits(Limits {
        body_timeout: Duration::from_millis(100),
        ..Limits::default()
    });
    let started = std::time::Instant::now();
    let resp = h.send(stalled_request(), "198.51.100.93:1").await;
    assert_eq!(resp.status(), StatusCode::SERVICE_UNAVAILABLE);
    let v: serde_json::Value =
        serde_json::from_slice(&resp.into_body().collect().await.unwrap().to_bytes()).unwrap();
    assert_eq!(v["error"], "timeout");
    assert!(started.elapsed() < Duration::from_secs(3));
}

#[tokio::test]
async fn one_client_cannot_hold_more_than_its_in_flight_cap() {
    let h = std::sync::Arc::new(Harness::with_limits(Limits {
        timeout: Duration::from_millis(600),
        max_per_client: 1,
        ..Limits::default()
    }));
    let h2 = h.clone();
    let slow = tokio::spawn(async move { h2.send(stalled_request(), "198.51.100.94:1").await });
    tokio::time::sleep(Duration::from_millis(50)).await;
    let renew = Client::new(1).renew_body(h.time());
    let (st, v) = h
        .call("POST", R, Some(&renew), "198.51.100.94:2", None)
        .await;
    assert_eq!(
        (st, v["error"].as_str()),
        (StatusCode::SERVICE_UNAVAILABLE, Some("busy"))
    );
    // Another client is unaffected.
    assert_eq!(
        h.call("POST", R, Some(&renew), "198.51.100.95:1", None)
            .await
            .0,
        StatusCode::NOT_FOUND
    );
    slow.await.unwrap();
    // The slot came back.
    assert_eq!(
        h.call("POST", R, Some(&renew), "198.51.100.94:3", None)
            .await
            .0,
        StatusCode::NOT_FOUND
    );
}
