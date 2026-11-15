//! Admin commands that work directly on the database.

use crate::abuse::{Ban, Net, BAN_MEMORY_SECS};
use crate::{api::unix_now, db, serial};
use rusqlite::Connection;

// Plain UTC calendar maths without a date library (H. Hinnant's civil algorithms).

/// Days since 1970-01-01 → (year, month, day).
fn civil_from_days(days: i64) -> (i64, i64, i64) {
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    (yoe + era * 400 + i64::from(m <= 2), m, d)
}

/// (year, month, day) → days since 1970-01-01.
fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let mp = if m > 2 { m - 3 } else { m + 9 };
    let doy = (153 * mp + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

fn days_in_month(y: i64, m: i64) -> i64 {
    days_from_civil(
        if m == 12 { y + 1 } else { y },
        if m == 12 { 1 } else { m + 1 },
        1,
    ) - days_from_civil(y, m, 1)
}

/// `t` plus `months` calendar months (UTC), the day clamped to the target month's length
/// (Jan 31 + 1 month = Feb 28/29).
pub fn add_months(t: i64, months: i64) -> i64 {
    let (y, m, d) = civil_from_days(t.div_euclid(86400));
    let idx = y * 12 + (m - 1) + months;
    let (ny, nm) = (idx.div_euclid(12), idx.rem_euclid(12) + 1);
    let nd = d.min(days_in_month(ny, nm));
    days_from_civil(ny, nm, nd) * 86400 + t.rem_euclid(86400)
}

/// `YYYY-MM-DD` → the end of that day (UTC), i.e. 00:00 of the next day, so the relay
/// works through the whole date.
pub fn parse_until_date(s: &str) -> Result<i64, String> {
    let bad = || format!("{s:?} is not a date (YYYY-MM-DD)");
    let parts: Vec<&str> = s.trim().split('-').collect();
    let [y, m, d] = parts.as_slice() else {
        return Err(bad());
    };
    if y.len() != 4 || m.len() != 2 || d.len() != 2 {
        return Err(bad());
    }
    let (y, m, d): (i64, i64, i64) = (
        y.parse().map_err(|_| bad())?,
        m.parse().map_err(|_| bad())?,
        d.parse().map_err(|_| bad())?,
    );
    if !(1..=12).contains(&m) || d < 1 || d > days_in_month(y, m) {
        return Err(bad());
    }
    Ok((days_from_civil(y, m, d) + 1) * 86400)
}

fn fmt_date(t: i64) -> String {
    let (y, m, d) = civil_from_days(t.div_euclid(86400));
    format!("{y:04}-{m:02}-{d:02}")
}

/// How the relay column reads: `-`, `until 2027-10-11` (last day included) or `ended 2026-…`.
fn fmt_relay(relay_until: Option<i64>, now: i64) -> String {
    match relay_until {
        None => "-".into(),
        // relay_until is exclusive; show the last day it works.
        Some(t) if t > now => format!("until {}", fmt_date(t - 1)),
        Some(t) => format!("ended {}", fmt_date(t - 1)),
    }
}

fn fmt_time(t: i64) -> String {
    let (y, m, d) = civil_from_days(t.div_euclid(86400));
    let secs = t.rem_euclid(86400);
    format!(
        "{y:04}-{m:02}-{d:02} {:02}:{:02}:{:02}Z",
        secs / 3600,
        secs / 60 % 60,
        secs % 60
    )
}

/// Finds a serial by its id or by the serial number itself.
pub fn find_serial(conn: &Connection, arg: &str) -> Result<db::Serial, String> {
    if let Some(s) = db::serial_by_id(conn, arg.trim()).map_err(|e| e.to_string())? {
        return Ok(s);
    }
    let norm = serial::normalise(arg)
        .ok_or_else(|| format!("{arg:?} is neither a serial id nor a valid serial number"))?;
    db::serial_by_hash(conn, &serial::hash(&norm))
        .map_err(|e| e.to_string())?
        .ok_or_else(|| "no such serial".to_string())
}

/// Creates a serial and returns (formatted serial, id). The serial itself is not stored.
pub fn create_serial(
    conn: &Connection,
    seats: i64,
    plan: &str,
    note: &str,
    relay_months: u32,
) -> Result<(String, String), String> {
    if seats < 1 {
        return Err("--seats must be at least 1".into());
    }
    if plan.trim().is_empty() {
        return Err("--plan must not be empty".into());
    }
    let now = unix_now();
    let relay_until = (relay_months > 0).then(|| add_months(now, relay_months as i64));
    // A collision is astronomically unlikely; retry anyway rather than fail.
    for _ in 0..5 {
        let norm = serial::generate();
        match db::insert_serial(
            conn,
            &serial::hash(&norm),
            &serial::hint(&norm),
            plan.trim(),
            seats,
            note,
            relay_until,
            now,
        ) {
            Ok(id) => return Ok((serial::format(&norm), id)),
            Err(rusqlite::Error::SqliteFailure(e, _))
                if e.code == rusqlite::ErrorCode::ConstraintViolation =>
            {
                continue
            }
            Err(e) => return Err(e.to_string()),
        }
    }
    Err("could not create a unique serial".into())
}

pub fn cmd_serial_create(
    conn: &Connection,
    seats: i64,
    plan: &str,
    note: &str,
    relay_months: u32,
) -> Result<(), String> {
    let (formatted, id) = create_serial(conn, seats, plan, note, relay_months)?;
    println!("{formatted}");
    let relay = db::serial_by_id(conn, &id)
        .map_err(|e| e.to_string())?
        .map(|s| fmt_relay(s.relay_until, unix_now()))
        .unwrap_or_default();
    eprintln!(
        "id {id} · plan {plan} · {seats} seats · relay {relay}. The serial is shown only now; it is not stored."
    );
    Ok(())
}

/// Changes to a serial's relay subscription.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum RelayChange {
    /// N months from the current end, or from now if it has ended or there is none.
    ExtendMonths(u32),
    /// Through this date (as from `parse_until_date`).
    Until(i64),
    Off,
}

/// Applies a relay change; returns the new `relay_until`.
pub fn set_relay(
    conn: &Connection,
    arg: &str,
    change: RelayChange,
) -> Result<(db::Serial, Option<i64>), String> {
    let s = find_serial(conn, arg)?;
    let now = unix_now();
    let new = match change {
        RelayChange::ExtendMonths(0) => return Err("--extend-months must be at least 1".into()),
        RelayChange::ExtendMonths(n) => {
            let from = s.relay_until.filter(|&t| t > now).unwrap_or(now);
            Some(add_months(from, n as i64))
        }
        RelayChange::Until(t) => Some(t),
        RelayChange::Off => None,
    };
    db::set_relay_until(conn, &s.id, new).map_err(|e| e.to_string())?;
    Ok((s, new))
}

pub fn cmd_serial_relay(conn: &Connection, arg: &str, change: RelayChange) -> Result<(), String> {
    let (s, new) = set_relay(conn, arg, change)?;
    println!(
        "{} ({}): relay {} (was {}). Installs get it with their next renewal (daily while online).",
        s.hint,
        s.id,
        fmt_relay(new, unix_now()),
        fmt_relay(s.relay_until, unix_now())
    );
    Ok(())
}

pub fn cmd_serial_list(conn: &Connection) -> Result<(), String> {
    let rows = db::list_serials(conn).map_err(|e| e.to_string())?;
    if rows.is_empty() {
        println!("no serials");
        return Ok(());
    }
    let now = unix_now();
    println!(
        "{:<36}  {:<12}  {:<10}  {:>5}  {:<8}  {:<17}  {:<20}  note",
        "id", "serial", "plan", "seats", "status", "relay", "created"
    );
    for (s, used) in rows {
        println!(
            "{:<36}  {:<12}  {:<10}  {:>5}  {:<8}  {:<17}  {:<20}  {}",
            s.id,
            s.hint,
            s.plan,
            format!("{used}/{}", s.seats),
            s.status,
            fmt_relay(s.relay_until, now),
            fmt_time(s.created_at),
            s.note
        );
    }
    Ok(())
}

pub fn cmd_serial_revoke(conn: &Connection, arg: &str) -> Result<(), String> {
    let s = find_serial(conn, arg)?;
    if db::revoke_serial(conn, &s.id, unix_now()).map_err(|e| e.to_string())? {
        println!("revoked {} ({}). Its installs stop working when their license expires (30 days at most).", s.hint, s.id);
    } else {
        println!("{} ({}) was already revoked", s.hint, s.id);
    }
    Ok(())
}

pub fn cmd_activations(conn: &Connection, arg: &str) -> Result<(), String> {
    let s = find_serial(conn, arg)?;
    let acts = db::all_activations(conn, &s.id).map_err(|e| e.to_string())?;
    let used = acts.iter().filter(|a| a.ended_at.is_none()).count();
    println!(
        "{} ({}) · {} · {used}/{} seats · {} · relay {}",
        s.hint,
        s.id,
        s.plan,
        s.seats,
        s.status,
        fmt_relay(s.relay_until, unix_now())
    );
    if acts.is_empty() {
        println!("no activations");
        return Ok(());
    }
    println!(
        "{:<36}  {:<8}  {:<10}  {:<20}  {:<20}  {:<10}  machine",
        "activation id", "status", "install", "activated", "last seen", "version"
    );
    for a in acts {
        let status = match a.ended_at {
            None => "active".to_string(),
            Some(_) => "ended".to_string(),
        };
        println!(
            "{:<36}  {:<8}  {:<10}  {:<20}  {:<20}  {:<10}  {}",
            a.id,
            status,
            &a.install_id[..a.install_id.len().min(8)],
            fmt_time(a.activated_at),
            fmt_time(a.last_seen_at),
            a.app_version,
            a.machine
        );
    }
    Ok(())
}

pub fn cmd_deactivate(conn: &Connection, id: &str) -> Result<(), String> {
    let a = db::activation_by_id(conn, id.trim())
        .map_err(|e| e.to_string())?
        .ok_or("no such activation")?;
    if db::end_activation(conn, &a.id, unix_now()).map_err(|e| e.to_string())? {
        println!("deactivated {} ({}); the seat is free. The install keeps working until its license expires.", a.id, a.machine);
    } else {
        println!("{} was already deactivated", a.id);
    }
    Ok(())
}

fn fmt_duration(secs: i64) -> String {
    if secs >= 86400 {
        format!("{}d{}h", secs / 86400, secs % 86400 / 3600)
    } else if secs >= 3600 {
        format!("{}h{}m", secs / 3600, secs % 3600 / 60)
    } else {
        format!("{}m", (secs + 59) / 60)
    }
}

pub fn cmd_ban_list(conn: &Connection) -> Result<(), String> {
    let now = unix_now();
    let bans = db::load_bans(conn).map_err(|e| e.to_string())?;
    if bans.is_empty() {
        println!("no bans");
        return Ok(());
    }
    println!(
        "{:<24}  {:<16}  {:<20}  {:>5}  {:<6}  {:<16}  note",
        "client", "status", "until", "count", "source", "last failure"
    );
    for b in bans {
        let status = if b.active(now) {
            format!("banned {}", fmt_duration(b.banned_until - now))
        } else {
            "ended".to_string()
        };
        println!(
            "{:<24}  {:<16}  {:<20}  {:>5}  {:<6}  {:<16}  {}",
            b.net.to_string(),
            status,
            fmt_time(b.banned_until),
            b.ban_count,
            b.source,
            b.last_code,
            b.note
        );
    }
    Ok(())
}

/// `interest list`: the newest sign-ups first.
pub fn cmd_interest_list(conn: &Connection, limit: i64) -> Result<(), String> {
    let rows = db::list_interests(conn, limit).map_err(|e| e.to_string())?;
    if rows.is_empty() {
        println!("no sign-ups yet");
        return Ok(());
    }
    println!(
        "{:<32}  {:<20}  {:>4}  {:<20}  {:<20}  note",
        "email", "name", "subs", "first seen", "last seen"
    );
    for r in rows {
        println!(
            "{:<32}  {:<20}  {:>4}  {:<20}  {:<20}  {}",
            r.email,
            r.name.unwrap_or_default(),
            r.submissions,
            fmt_time(r.created_at),
            fmt_time(r.updated_at),
            r.note.unwrap_or_default()
        );
    }
    Ok(())
}

/// `interest export`: every sign-up as CSV on stdout, oldest first. RFC 4180: CRLF after each
/// record, fields containing a comma, quote or line break are quoted, quotes are doubled.
pub fn cmd_interest_export(conn: &Connection) -> Result<(), String> {
    let rows = db::export_interests(conn).map_err(|e| e.to_string())?;
    let mut out = String::from("email,name,note,submissions,first_seen,last_seen\r\n");
    for r in rows {
        let fields = [
            csv_field(&r.email),
            csv_field(r.name.as_deref().unwrap_or("")),
            csv_field(r.note.as_deref().unwrap_or("")),
            r.submissions.to_string(),
            iso_utc(r.created_at),
            iso_utc(r.updated_at),
        ];
        out.push_str(&fields.join(","));
        out.push_str("\r\n");
    }
    print!("{out}");
    Ok(())
}

/// One CSV field. A value a spreadsheet would read as a formula (starts with `=`, `+`, `-`,
/// `@`, tab or CR) gets a leading single quote, so a sign-up can't run anything when opened.
fn csv_field(s: &str) -> String {
    let guarded;
    let s = if s.starts_with(['=', '+', '-', '@', '\t', '\r']) {
        guarded = format!("'{s}");
        guarded.as_str()
    } else {
        s
    };
    if s.contains([',', '"', '\r', '\n']) {
        format!("\"{}\"", s.replace('"', "\"\""))
    } else {
        s.to_string()
    }
}

/// `2026-10-11T14:45:00Z`, the form spreadsheets and other tools read reliably.
fn iso_utc(t: i64) -> String {
    fmt_time(t).replace(' ', "T")
}

pub fn cmd_ban_add(conn: &Connection, net: &str, hours: f64, note: &str) -> Result<(), String> {
    let net = Net::parse(net)?;
    if !(hours > 0.0 && hours <= 24.0 * 3650.0) {
        return Err("--hours must be more than 0 (and at most 10 years)".into());
    }
    let now = unix_now();
    let prev = db::load_bans(conn)
        .map_err(|e| e.to_string())?
        .into_iter()
        .find(|b| b.net == net && b.last_ban_at > now - BAN_MEMORY_SECS);
    let ban = Ban {
        net,
        banned_until: now + (hours * 3600.0).round() as i64,
        ban_count: prev.as_ref().map_or(0, |b| b.ban_count) + 1,
        last_ban_at: now,
        last_code: prev.map(|b| b.last_code).unwrap_or_default(),
        source: "admin".into(),
        note: note.to_string(),
    };
    db::upsert_ban(conn, &ban, now).map_err(|e| e.to_string())?;
    println!(
        "banned {net} until {} ({}). A running server picks this up within 30 s.",
        fmt_time(ban.banned_until),
        fmt_duration(ban.banned_until - now)
    );
    Ok(())
}

pub fn cmd_ban_remove(conn: &Connection, net: &str) -> Result<(), String> {
    let net = Net::parse(net)?;
    if db::delete_ban(conn, &net).map_err(|e| e.to_string())? {
        println!("removed the ban on {net}. A running server picks this up within 30 s.");
        Ok(())
    } else {
        Err(format!("no ban on {net} (see `ban list`)"))
    }
}

pub fn cmd_stats(conn: &Connection) -> Result<(), String> {
    let now = unix_now();
    let since = now - 24 * 3600;
    let rows = db::stats_since(conn, since).map_err(|e| e.to_string())?;
    let get = |k: &str| {
        rows.iter()
            .find(|(kind, _)| kind == k)
            .map_or(0, |(_, n)| *n)
    };
    let new_seats = db::count_new_activations_since(conn, since).map_err(|e| e.to_string())?;
    let active_bans = db::load_bans(conn)
        .map_err(|e| e.to_string())?
        .iter()
        .filter(|b| b.active(now))
        .count();
    println!("last 24 hours (the server writes its counters every 30 s):");
    println!(
        "  activations      {} ({new_seats} new seats)",
        get("activate_ok")
    );
    println!("  renewals         {}", get("renew_ok"));
    println!("  deactivations    {}", get("deactivate_ok"));
    println!("  failures         {}", get("failure"));
    for (kind, n) in rows.iter().filter(|(k, _)| k.starts_with("failure:")) {
        println!("    {:<18} {n}", &kind["failure:".len()..]);
    }
    println!(
        "  bans             {} (active now: {active_bans})",
        get("ban")
    );
    println!("  blocked requests {}", get("temporarily_blocked"));
    println!("  rate limited     {}", get("rate_limited"));
    println!("  brake (busy)     {}", get("busy"));
    for k in [
        "seats_full",
        "not_activated",
        "payload_too_large",
        "overloaded",
    ] {
        let n = get(k);
        if n > 0 {
            println!("  {k:<16} {n}");
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn csv_fields_are_quoted_and_formulas_defused() {
        assert_eq!(csv_field("plain"), "plain");
        assert_eq!(csv_field("a,b"), "\"a,b\"");
        assert_eq!(csv_field("say \"hi\""), "\"say \"\"hi\"\"\"");
        assert_eq!(csv_field("=HYPERLINK(\"x\")"), "\"'=HYPERLINK(\"\"x\"\")\"");
        assert_eq!(csv_field("+1"), "'+1");
        assert_eq!(csv_field("-2"), "'-2");
        assert_eq!(csv_field("@SUM(A1)"), "'@SUM(A1)");
        assert_eq!(csv_field("\tx"), "'\tx");
        assert_eq!(csv_field("\rx"), "\"'\rx\"");
        assert_eq!(csv_field("a=b"), "a=b");
    }

    #[test]
    fn time_format() {
        assert_eq!(fmt_time(0), "1970-01-01 00:00:00Z");
        assert_eq!(fmt_time(1_792_000_000), "2026-10-14 17:46:40Z");
    }

    #[test]
    fn create_find_revoke() {
        let dir = tempfile::tempdir().unwrap();
        let c = db::open(&dir.path().join("l.db")).unwrap();
        let (formatted, id) = create_serial(&c, 3, "standard", "test", 0).unwrap();
        assert!(formatted.starts_with("EGG-") && formatted.len() == 27);
        assert_eq!(find_serial(&c, &formatted).unwrap().id, id);
        assert_eq!(find_serial(&c, &formatted.to_lowercase()).unwrap().id, id);
        assert_eq!(find_serial(&c, &id).unwrap().id, id);
        // Nothing stores the serial in clear.
        let n: i64 = c
            .query_row(
                "SELECT COUNT(*) FROM serials WHERE hash = ?1 OR hint = ?1 OR note = ?1",
                [&formatted],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n, 0);
        cmd_serial_revoke(&c, &id).unwrap();
        assert_eq!(find_serial(&c, &id).unwrap().status, "revoked");
        assert!(create_serial(&c, 0, "standard", "", 0).is_err());
    }

    #[test]
    fn calendar() {
        let t = 1_792_000_000; // 2026-10-14 17:46:40Z
        assert_eq!(fmt_time(add_months(t, 12)), "2027-10-14 17:46:40Z");
        assert_eq!(fmt_time(add_months(t, 3)), "2027-01-14 17:46:40Z");
        let jan31 = parse_until_date("2027-01-31").unwrap() - 86400;
        assert_eq!(fmt_time(add_months(jan31, 1)), "2027-02-28 00:00:00Z");
        assert_eq!(fmt_time(add_months(jan31, 13)), "2028-02-29 00:00:00Z");
        assert_eq!(
            fmt_time(parse_until_date("2027-10-11").unwrap()),
            "2027-10-12 00:00:00Z"
        );
        for bad in ["2027-13-01", "2027-02-29", "27-01-01", "2027/01/01", "x"] {
            assert!(parse_until_date(bad).is_err(), "{bad}");
        }
        for d in -800_000..800_000i64 {
            if d % 997 == 0 {
                let (y, m, dd) = civil_from_days(d);
                assert_eq!(days_from_civil(y, m, dd), d);
            }
        }
        assert_eq!(fmt_relay(None, t), "-");
        assert_eq!(
            fmt_relay(Some(parse_until_date("2027-10-11").unwrap()), t),
            "until 2027-10-11"
        );
        assert_eq!(fmt_relay(Some(t - 86400), t), "ended 2026-10-13");
    }

    #[test]
    fn relay_create_extend_until_off() {
        let dir = tempfile::tempdir().unwrap();
        let c = db::open(&dir.path().join("l.db")).unwrap();
        let now = unix_now();
        let (none, none_id) = create_serial(&c, 3, "standard", "", 0).unwrap();
        assert_eq!(find_serial(&c, &none).unwrap().relay_until, None);
        let (with, _) = create_serial(&c, 3, "standard", "", 12).unwrap();
        let until = find_serial(&c, &with).unwrap().relay_until.unwrap();
        assert!((until - add_months(now, 12)).abs() <= 2);

        // Extending a running subscription adds to its end; an ended or missing one starts now.
        let (_, new) = set_relay(&c, &with, RelayChange::ExtendMonths(12)).unwrap();
        assert_eq!(new, Some(add_months(until, 12)));
        let (_, new) = set_relay(&c, &none_id, RelayChange::ExtendMonths(1)).unwrap();
        assert!((new.unwrap() - add_months(now, 1)).abs() <= 2);
        db::set_relay_until(&c, &none_id, Some(now - 100)).unwrap();
        let (_, new) = set_relay(&c, &none_id, RelayChange::ExtendMonths(1)).unwrap();
        assert!((new.unwrap() - add_months(now, 1)).abs() <= 2);

        let d = parse_until_date("2030-01-15").unwrap();
        assert_eq!(
            set_relay(&c, &none, RelayChange::Until(d)).unwrap().1,
            Some(d)
        );
        assert_eq!(find_serial(&c, &none).unwrap().relay_until, Some(d));
        assert_eq!(set_relay(&c, &none, RelayChange::Off).unwrap().1, None);
        assert_eq!(find_serial(&c, &none).unwrap().relay_until, None);
        assert!(set_relay(&c, &none, RelayChange::ExtendMonths(0)).is_err());
        cmd_serial_list(&c).unwrap();
    }

    #[test]
    fn ban_add_list_remove() {
        let dir = tempfile::tempdir().unwrap();
        let c = db::open(&dir.path().join("l.db")).unwrap();
        cmd_ban_add(&c, "203.0.113.7", 2.0, "abuse").unwrap();
        cmd_ban_add(&c, "2001:db8:1:2::99", 1.0, "").unwrap();
        cmd_ban_add(&c, "10.1.0.0/16", 1.0, "").unwrap();
        assert!(cmd_ban_add(&c, "not-an-ip", 1.0, "").is_err());
        assert!(cmd_ban_add(&c, "1.2.3.4", 0.0, "").is_err());
        let bans = db::load_bans(&c).unwrap();
        let nets: Vec<String> = bans.iter().map(|b| b.net.to_string()).collect();
        for n in ["203.0.113.7/32", "2001:db8:1:2::/64", "10.1.0.0/16"] {
            assert!(nets.contains(&n.to_string()), "{n}");
        }
        let find = |n: &str| {
            db::load_bans(&c)
                .unwrap()
                .into_iter()
                .find(|b| b.net.to_string() == n)
                .unwrap()
        };
        let b = find("203.0.113.7/32");
        assert_eq!(
            (b.source.as_str(), b.note.as_str(), b.ban_count),
            ("admin", "abuse", 1)
        );
        assert!((b.banned_until - unix_now() - 7200).abs() <= 2);
        // Adding again escalates the remembered count.
        cmd_ban_add(&c, "203.0.113.7", 1.0, "again").unwrap();
        assert_eq!(find("203.0.113.7/32").ban_count, 2);
        cmd_ban_list(&c).unwrap();
        cmd_ban_remove(&c, "203.0.113.7/32").unwrap();
        cmd_ban_remove(&c, "2001:db8:1:2::1").unwrap();
        assert!(cmd_ban_remove(&c, "203.0.113.7").is_err());
        assert_eq!(db::load_bans(&c).unwrap().len(), 1);
        cmd_stats(&c).unwrap();
    }
}
