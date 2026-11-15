//! Abuse protection: per-client failure tracking and escalating bans, plus a global brake on
//! failed activations (docs/licensing-protocol.md, "Abuse protection").
//!
//! All state lives in memory behind one mutex. Bans are written through to SQLite by the caller
//! (see `api.rs`), and the cache is reloaded from the database periodically so admin changes
//! made with the CLI reach a running server.

use std::collections::{HashMap, VecDeque};
use std::fmt;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};
use std::sync::Mutex;

/// Failures that count towards a ban. `stale_timestamp` is not one: it is only answered for a
/// validly signed request, and a wrong clock must not get a whole shared network banned.
pub const FAILURE_CODES: [&str; 5] = [
    "invalid_serial",
    "unknown_serial",
    "bad_signature",
    "serial_revoked",
    "bad_request",
];
/// Failures within `FAIL_WINDOW_SECS` that trigger a ban.
pub const FAIL_LIMIT: usize = 5;
pub const FAIL_WINDOW_SECS: i64 = 15 * 60;
/// First ban; every further ban of the same client doubles, up to `BAN_MAX_SECS`.
pub const BAN_BASE_SECS: i64 = 60 * 60;
pub const BAN_MAX_SECS: i64 = 7 * 24 * 60 * 60;
/// How long a client's ban count is remembered after its last ban.
pub const BAN_MEMORY_SECS: i64 = 30 * 24 * 60 * 60;
/// Global brake: more than `GLOBAL_LIMIT` `unknown_serial` answers (the brute-force signal)
/// within `GLOBAL_WINDOW_SECS`.
pub const GLOBAL_LIMIT: usize = 100;
pub const GLOBAL_WINDOW_SECS: i64 = 60 * 60;
/// Bound on the global failure log (it stops growing while the brake is on anyway).
const GLOBAL_MAX_ENTRIES: usize = 10_000;

pub fn is_failure(code: &str) -> bool {
    FAILURE_CODES.contains(&code)
}

/// Length of the `count`-th ban (1-based): 1 h, 2 h, 4 h, … capped at 7 days.
pub fn ban_duration(count: i64) -> i64 {
    let shift = (count.max(1) - 1).min(20) as u32;
    (BAN_BASE_SECS << shift).min(BAN_MAX_SECS)
}

// ---------- networks ----------

/// An address block: a client (IPv4 /32, IPv6 /64) or an admin-entered CIDR.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct Net {
    addr: IpAddr,
    prefix: u8,
}

fn unmap(ip: IpAddr) -> IpAddr {
    match ip {
        IpAddr::V6(v6) => v6
            .to_ipv4_mapped()
            .map(IpAddr::V4)
            .unwrap_or(IpAddr::V6(v6)),
        v4 => v4,
    }
}

fn mask(ip: IpAddr, prefix: u8) -> IpAddr {
    match ip {
        IpAddr::V4(v4) => {
            let m = if prefix == 0 {
                0
            } else {
                u32::MAX << (32 - prefix as u32)
            };
            IpAddr::V4(Ipv4Addr::from(u32::from(v4) & m))
        }
        IpAddr::V6(v6) => {
            let m = if prefix == 0 {
                0
            } else {
                u128::MAX << (128 - prefix as u32)
            };
            IpAddr::V6(Ipv6Addr::from(u128::from(v6) & m))
        }
    }
}

impl Net {
    /// The block a client is tracked and banned by: IPv4 /32, IPv6 /64.
    pub fn client(ip: IpAddr) -> Net {
        let ip = unmap(ip);
        let prefix = if ip.is_ipv4() { 32 } else { 64 };
        Net {
            addr: mask(ip, prefix),
            prefix,
        }
    }

    /// `1.2.3.4` / `2001:db8::1` (the client block, as above) or `1.2.3.0/24` / `2001:db8::/48`.
    pub fn parse(s: &str) -> Result<Net, String> {
        let s = s.trim();
        match s.split_once('/') {
            None => s
                .parse::<IpAddr>()
                .map(Net::client)
                .map_err(|_| format!("{s:?} is not an IP address or CIDR")),
            Some((a, p)) => {
                let ip = unmap(
                    a.parse::<IpAddr>()
                        .map_err(|_| format!("{a:?} is not an IP address"))?,
                );
                let max = if ip.is_ipv4() { 32 } else { 128 };
                let prefix: u8 = p
                    .parse()
                    .ok()
                    .filter(|&p| p <= max)
                    .ok_or_else(|| format!("bad prefix length {p:?}"))?;
                Ok(Net {
                    addr: mask(ip, prefix),
                    prefix,
                })
            }
        }
    }

    pub fn contains(&self, ip: IpAddr) -> bool {
        let ip = unmap(ip);
        ip.is_ipv4() == self.addr.is_ipv4() && mask(ip, self.prefix) == self.addr
    }

    fn is_client_shape(&self) -> bool {
        self.prefix == if self.addr.is_ipv4() { 32 } else { 64 }
    }
}

impl fmt::Display for Net {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}/{}", self.addr, self.prefix)
    }
}

// ---------- bans ----------

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Ban {
    pub net: Net,
    pub banned_until: i64,
    pub ban_count: i64,
    pub last_ban_at: i64,
    pub last_code: String,
    /// `auto` or `admin`.
    pub source: String,
    pub note: String,
}

impl Ban {
    pub fn active(&self, now: i64) -> bool {
        now < self.banned_until
    }
}

#[derive(Default)]
struct State {
    failures: HashMap<Net, VecDeque<i64>>,
    bans: HashMap<Net, Ban>,
    /// Bans whose block isn't a client block (admin CIDRs); checked by containment.
    wide: Vec<Net>,
    global: VecDeque<i64>,
    braked: bool,
    /// Auto bans whose database write failed; kept (and merged over reloads) until saved.
    unsaved: HashMap<Net, Ban>,
    /// Log-line throttle: key to the time its quiet period ends.
    quiet: HashMap<String, i64>,
    /// (unix hour, kind) → count, flushed to `request_stats`.
    counters: HashMap<(i64, String), i64>,
}

impl State {
    fn rebuild_wide(&mut self) {
        self.wide = self
            .bans
            .keys()
            .filter(|n| !n.is_client_shape())
            .copied()
            .collect();
    }

    fn count(&mut self, now: i64, kind: &str) {
        *self
            .counters
            .entry((now.div_euclid(3600), kind.to_string()))
            .or_default() += 1;
    }

    fn prune_global(&mut self, now: i64) {
        while self
            .global
            .front()
            .is_some_and(|&t| t <= now - GLOBAL_WINDOW_SECS)
        {
            self.global.pop_front();
        }
    }
}

#[derive(Default)]
pub struct Abuse {
    state: Mutex<State>,
}

impl Abuse {
    pub fn new() -> Self {
        Self::default()
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Replaces the ban cache (from the database).
    pub fn load_bans(&self, bans: Vec<Ban>) {
        let mut st = self.lock();
        st.bans = bans.into_iter().map(|b| (b.net, b)).collect();
        let unsaved: Vec<Ban> = st.unsaved.values().cloned().collect();
        for b in unsaved {
            st.bans.insert(b.net, b);
        }
        st.rebuild_wide();
    }

    /// Keeps a ban whose database write failed in memory until `mark_saved`.
    pub fn mark_unsaved(&self, ban: Ban) {
        let mut st = self.lock();
        st.unsaved.insert(ban.net, ban);
    }

    pub fn mark_saved(&self, net: &Net) {
        self.lock().unsaved.remove(net);
    }

    /// Bans still waiting for a successful database write.
    pub fn unsaved(&self) -> Vec<Ban> {
        self.lock().unsaved.values().cloned().collect()
    }

    /// True for the first call per `key` within `ttl` seconds: used to log one line per ban or
    /// per rate-limit window instead of one per request.
    pub fn should_log(&self, key: &str, ttl: i64, now: i64) -> bool {
        let mut st = self.lock();
        if st.quiet.get(key).is_some_and(|&until| until > now) {
            return false;
        }
        if st.quiet.len() >= 10_000 {
            st.quiet.retain(|_, &mut until| until > now);
        }
        if st.quiet.len() < 10_000 {
            st.quiet.insert(key.to_string(), now + ttl.max(1));
        }
        true
    }

    /// Seconds until the ban covering `ip` ends, if one is active.
    pub fn banned(&self, ip: IpAddr, now: i64) -> Option<i64> {
        let st = self.lock();
        let mut until = st
            .bans
            .get(&Net::client(ip))
            .filter(|b| b.active(now))
            .map(|b| b.banned_until);
        for n in &st.wide {
            if n.contains(ip) {
                if let Some(b) = st.bans.get(n).filter(|b| b.active(now)) {
                    until = Some(until.map_or(b.banned_until, |u| u.max(b.banned_until)));
                }
            }
        }
        until.map(|u| (u - now).max(1))
    }

    /// Records a failed request; returns the new ban when this failure triggered one
    /// (the caller persists it). `reload` reads the client's current ban row from the database
    /// (the caller holds the database lock), so escalation never uses a stale cache; it
    /// returns `None` when there is no row or the read failed, and then the cache is used.
    pub fn record_failure(
        &self,
        ip: IpAddr,
        code: &str,
        now: i64,
        reload: impl FnOnce(&Net) -> Option<Ban>,
    ) -> Option<Ban> {
        let mut st = self.lock();
        st.count(now, "failure");
        st.count(now, &format!("failure:{code}"));
        if code == "unknown_serial" {
            st.prune_global(now);
            st.global.push_back(now);
            if st.global.len() > GLOBAL_MAX_ENTRIES {
                st.global.pop_front();
            }
            if st.global.len() > GLOBAL_LIMIT && !st.braked {
                st.braked = true;
                println!(
                    "brake on: more than {GLOBAL_LIMIT} unknown serials in the last hour; activate answers 503 busy"
                );
            }
        }

        let net = Net::client(ip);
        let window = st.failures.entry(net).or_default();
        while window.front().is_some_and(|&t| t <= now - FAIL_WINDOW_SECS) {
            window.pop_front();
        }
        window.push_back(now);
        if window.len() < FAIL_LIMIT {
            return None;
        }
        st.failures.remove(&net);

        let prev = reload(&net).or_else(|| st.bans.get(&net).cloned());
        let prev_count = prev
            .as_ref()
            .filter(|b| b.last_ban_at > now - BAN_MEMORY_SECS)
            .map_or(0, |b| b.ban_count);
        let count = prev_count + 1;
        let duration = ban_duration(count);
        let ban = Ban {
            net,
            banned_until: (now + duration).max(prev.as_ref().map_or(0, |b| b.banned_until)),
            ban_count: count,
            last_ban_at: now,
            last_code: code.to_string(),
            source: "auto".into(),
            note: prev.map(|b| b.note).unwrap_or_default(),
        };
        st.bans.insert(net, ban.clone());
        st.count(now, "ban");
        println!("ban client={net} count={count} duration={duration}s last_failure={code}");
        Some(ban)
    }

    /// A completed activate or renew clears the client's short failure window (not its bans).
    /// Other successes (deactivate, interest) must not: they need no serial, so they would
    /// let a client interleave failures forever without being banned.
    pub fn clear_failures(&self, ip: IpAddr) {
        self.lock().failures.remove(&Net::client(ip));
    }

    /// Counts an outcome for `stats` (`activate_ok`, `rate_limited`, …).
    pub fn count(&self, kind: &str, now: i64) {
        self.lock().count(now, kind);
    }

    /// Global brake on activations: `Some(retry_after)` while more than `GLOBAL_LIMIT`
    /// failed activations happened in the last hour.
    pub fn brake(&self, now: i64) -> Option<i64> {
        let mut st = self.lock();
        st.prune_global(now);
        let n = st.global.len();
        if n > GLOBAL_LIMIT {
            if !st.braked {
                st.braked = true;
                println!("brake on: more than {GLOBAL_LIMIT} unknown serials in the last hour; activate answers 503 busy");
            }
            // The count is back to the limit once the (n - limit) oldest entries have aged out.
            let t = st.global[n - GLOBAL_LIMIT - 1];
            return Some((t + GLOBAL_WINDOW_SECS - now).max(1));
        }
        if st.braked {
            st.braked = false;
            println!("brake off: unknown serials in the last hour are back to {n}");
        }
        None
    }

    /// Drops idle failure windows; re-checks the brake (so its "off" line is logged).
    pub fn prune(&self, now: i64) {
        {
            let mut st = self.lock();
            st.failures
                .retain(|_, w| w.back().is_some_and(|&t| t > now - FAIL_WINDOW_SECS));
            st.quiet.retain(|_, &mut until| until > now);
        }
        self.brake(now);
    }

    /// Takes the counters for flushing to the database.
    pub fn take_counters(&self) -> Vec<(i64, String, i64)> {
        self.lock()
            .counters
            .drain()
            .map(|((h, k), n)| (h, k, n))
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ip(s: &str) -> IpAddr {
        s.parse().unwrap()
    }

    #[test]
    fn durations_double_and_cap() {
        assert_eq!(ban_duration(1), 3600);
        assert_eq!(ban_duration(2), 7200);
        assert_eq!(ban_duration(3), 4 * 3600);
        assert_eq!(ban_duration(8), 128 * 3600);
        assert_eq!(ban_duration(9), BAN_MAX_SECS);
        assert_eq!(ban_duration(1000), BAN_MAX_SECS);
    }

    #[test]
    fn nets() {
        assert_eq!(Net::parse("1.2.3.4").unwrap().to_string(), "1.2.3.4/32");
        assert_eq!(
            Net::parse("2001:db8:1:2:3::9").unwrap().to_string(),
            "2001:db8:1:2::/64"
        );
        assert_eq!(Net::parse("10.1.2.3/8").unwrap().to_string(), "10.0.0.0/8");
        assert_eq!(
            Net::parse("::ffff:1.2.3.4").unwrap().to_string(),
            "1.2.3.4/32"
        );
        assert!(Net::parse("1.2.3.4/33").is_err());
        assert!(Net::parse("nope").is_err());
        let n = Net::parse("10.0.0.0/8").unwrap();
        assert!(
            n.contains(ip("10.9.9.9")) && !n.contains(ip("11.0.0.1")) && !n.contains(ip("::1"))
        );
        assert_eq!(
            Net::client(ip("2001:db8:1:2::1")),
            Net::client(ip("2001:db8:1:2:ffff::"))
        );
    }

    #[test]
    fn five_failures_ban_and_success_resets_window() {
        let a = Abuse::new();
        let c = ip("203.0.113.1");
        for _ in 0..4 {
            assert!(a
                .record_failure(c, "unknown_serial", 1000, |_| None)
                .is_none());
        }
        a.clear_failures(c);
        for _ in 0..4 {
            assert!(a
                .record_failure(c, "unknown_serial", 1000, |_| None)
                .is_none());
        }
        let b = a
            .record_failure(c, "bad_signature", 1000, |_| None)
            .unwrap();
        assert_eq!(
            (b.ban_count, b.banned_until, b.last_code.as_str()),
            (1, 4600, "bad_signature")
        );
        assert_eq!(a.banned(c, 1000), Some(3600));
        assert_eq!(a.banned(c, 4600), None);
        // A success doesn't lift the ban.
        a.clear_failures(c);
        assert!(a.banned(c, 2000).is_some());
    }

    #[test]
    fn failures_outside_the_window_do_not_count() {
        let a = Abuse::new();
        let c = ip("203.0.113.1");
        for i in 0..4 {
            a.record_failure(c, "bad_request", i * 300, |_| None);
        }
        // The first one (t=0) is 15 min old at t=900.
        assert!(a.record_failure(c, "bad_request", 900, |_| None).is_none());
        assert!(a.record_failure(c, "bad_request", 901, |_| None).is_some());
    }

    #[test]
    fn escalation_cap_and_memory() {
        let a = Abuse::new();
        let c = ip("198.51.100.4");
        let mut now = 0;
        let mut seen = vec![];
        for _ in 0..10 {
            let mut ban = None;
            for _ in 0..FAIL_LIMIT {
                ban = a.record_failure(c, "unknown_serial", now, |_| None);
            }
            let b = ban.unwrap();
            seen.push(b.banned_until - now);
            now = b.banned_until;
        }
        assert_eq!(seen[..4], [3600, 7200, 14400, 28800]);
        assert_eq!(seen[9], BAN_MAX_SECS);
        // 30 days after the last ban the count is forgotten.
        now += BAN_MEMORY_SECS;
        let mut ban = None;
        for _ in 0..FAIL_LIMIT {
            ban = a.record_failure(c, "unknown_serial", now, |_| None);
        }
        assert_eq!(ban.unwrap().ban_count, 1);
    }

    #[test]
    fn wide_ban_covers_the_block() {
        let a = Abuse::new();
        a.load_bans(vec![Ban {
            net: Net::parse("10.0.0.0/8").unwrap(),
            banned_until: 500,
            ban_count: 1,
            last_ban_at: 0,
            last_code: String::new(),
            source: "admin".into(),
            note: String::new(),
        }]);
        assert_eq!(a.banned(ip("10.2.3.4"), 100), Some(400));
        assert_eq!(a.banned(ip("11.2.3.4"), 100), None);
    }

    #[test]
    fn brake_trips_and_clears() {
        let a = Abuse::new();
        for i in 0..=GLOBAL_LIMIT as i64 {
            let c = IpAddr::V4(Ipv4Addr::from(0x0a00_0000 + i as u32));
            a.record_failure(c, "unknown_serial", 100 + i, |_| None);
        }
        // 101 failures at t=100..=200: on until the first one ages out at t=3700.
        assert_eq!(a.brake(300), Some(3400));
        assert_eq!(a.brake(3699), Some(1));
        assert_eq!(a.brake(3700), None);
        // Only unknown_serial feeds the brake.
        let b = Abuse::new();
        for i in 0..200u32 {
            b.record_failure(IpAddr::V4(Ipv4Addr::from(i)), "bad_signature", 1, |_| None);
        }
        assert_eq!(b.brake(2), None);
    }

    #[test]
    fn brake_counts_only_unknown_serial() {
        let a = Abuse::new();
        for i in 0..200u32 {
            let c = IpAddr::V4(Ipv4Addr::from(i));
            for code in ["invalid_serial", "bad_request", "serial_revoked"] {
                a.record_failure(c, code, 1, |_| None);
            }
        }
        assert_eq!(a.brake(2), None);
    }

    #[test]
    fn escalation_uses_the_reloaded_row_not_the_cache() {
        let a = Abuse::new();
        let c = ip("203.0.113.5");
        let row = Ban {
            net: Net::client(c),
            banned_until: 0,
            ban_count: 3,
            last_ban_at: 900,
            last_code: String::new(),
            source: "auto".into(),
            note: String::new(),
        };
        let mut ban = None;
        for _ in 0..FAIL_LIMIT {
            let r = row.clone();
            ban = a.record_failure(c, "bad_request", 1000, move |_| Some(r));
        }
        let ban = ban.unwrap();
        assert_eq!(ban.ban_count, 4);
        assert_eq!(ban.banned_until, 1000 + 8 * 3600);
    }

    #[test]
    fn unsaved_bans_survive_a_cache_reload_until_saved() {
        let a = Abuse::new();
        let c = ip("203.0.113.6");
        let mut ban = None;
        for _ in 0..FAIL_LIMIT {
            ban = a.record_failure(c, "bad_request", 1000, |_| None);
        }
        let ban = ban.unwrap();
        a.mark_unsaved(ban.clone());
        a.load_bans(vec![]);
        assert!(a.banned(c, 1001).is_some());
        a.mark_saved(&ban.net);
        a.load_bans(vec![]);
        assert!(a.banned(c, 1001).is_none());
    }

    #[test]
    fn should_log_once_per_quiet_period() {
        let a = Abuse::new();
        assert!(a.should_log("k", 60, 100));
        assert!(!a.should_log("k", 60, 159));
        assert!(a.should_log("k", 60, 160));
        assert!(a.should_log("other", 60, 100));
    }
}
