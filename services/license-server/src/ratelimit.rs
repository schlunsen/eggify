//! In-memory per-IP token buckets.

use std::collections::HashMap;
use std::net::IpAddr;
use std::sync::Mutex;
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Class {
    /// `activate`: 10 per minute.
    Activate,
    /// `renew`, `deactivate`: 60 per minute.
    Other,
    /// `interest` (the website form): 5 per minute. No failure brake, since people mistype.
    Interest,
}

impl Class {
    fn per_minute(self) -> f64 {
        match self {
            Class::Activate => 10.0,
            Class::Other => 60.0,
            Class::Interest => 5.0,
        }
    }
}

struct Bucket {
    tokens: f64,
    updated: Instant,
}

/// Each (client, class) gets a bucket holding up to a minute's allowance,
/// refilled continuously at that rate.
#[derive(Default)]
pub struct RateLimiter {
    buckets: Mutex<HashMap<(IpAddr, Class), Bucket>>,
}

/// IPv6 clients are keyed by their /64 (one host usually has the whole /64).
fn client_key(ip: IpAddr) -> IpAddr {
    match ip {
        IpAddr::V6(v6) => {
            if let Some(v4) = v6.to_ipv4_mapped() {
                return IpAddr::V4(v4);
            }
            let mut seg = v6.segments();
            seg[4..].fill(0);
            IpAddr::V6(seg.into())
        }
        v4 => v4,
    }
}

impl RateLimiter {
    pub fn new() -> Self {
        Self::default()
    }

    /// Takes one token; false when the bucket is empty.
    pub fn check(&self, ip: IpAddr, class: Class) -> bool {
        self.check_at(ip, class, Instant::now())
    }

    pub fn check_at(&self, ip: IpAddr, class: Class, now: Instant) -> bool {
        let cap = class.per_minute();
        let mut map = self.buckets.lock().unwrap_or_else(|e| e.into_inner());
        let b = map.entry((client_key(ip), class)).or_insert(Bucket {
            tokens: cap,
            updated: now,
        });
        let elapsed = now.saturating_duration_since(b.updated).as_secs_f64();
        b.tokens = (b.tokens + elapsed * cap / 60.0).min(cap);
        b.updated = now;
        if b.tokens >= 1.0 {
            b.tokens -= 1.0;
            true
        } else {
            false
        }
    }

    /// Drops buckets that have been idle long enough to be full again.
    pub fn prune(&self) {
        let now = Instant::now();
        let mut map = self.buckets.lock().unwrap_or_else(|e| e.into_inner());
        map.retain(|_, b| now.saturating_duration_since(b.updated) < Duration::from_secs(60));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn activate_allows_ten_per_minute_then_refills() {
        let rl = RateLimiter::new();
        let ip: IpAddr = "203.0.113.5".parse().unwrap();
        let t0 = Instant::now();
        for _ in 0..10 {
            assert!(rl.check_at(ip, Class::Activate, t0));
        }
        assert!(!rl.check_at(ip, Class::Activate, t0));
        // Other class and other IPs are separate.
        assert!(rl.check_at(ip, Class::Other, t0));
        assert!(rl.check_at("203.0.113.6".parse().unwrap(), Class::Activate, t0));
        // One token back after 6 s.
        assert!(rl.check_at(ip, Class::Activate, t0 + Duration::from_secs(6)));
        assert!(!rl.check_at(ip, Class::Activate, t0 + Duration::from_secs(6)));
    }

    #[test]
    fn interest_allows_five_per_minute_and_is_separate() {
        let rl = RateLimiter::new();
        let ip: IpAddr = "203.0.113.8".parse().unwrap();
        let t0 = Instant::now();
        for _ in 0..5 {
            assert!(rl.check_at(ip, Class::Interest, t0));
        }
        assert!(!rl.check_at(ip, Class::Interest, t0));
        // Its own bucket: the activate and other classes are not drained by it.
        assert!(rl.check_at(ip, Class::Activate, t0));
        assert!(rl.check_at(ip, Class::Other, t0));
    }

    #[test]
    fn ipv6_keyed_by_64() {
        let rl = RateLimiter::new();
        let t0 = Instant::now();
        for i in 0..10 {
            let ip: IpAddr = format!("2001:db8:1:2::{i}").parse().unwrap();
            assert!(rl.check_at(ip, Class::Activate, t0));
        }
        assert!(!rl.check_at("2001:db8:1:2:ffff::1".parse().unwrap(), Class::Activate, t0));
        assert!(rl.check_at("2001:db8:1:3::1".parse().unwrap(), Class::Activate, t0));
    }
}
