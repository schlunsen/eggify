//! SQLite storage: serials and activations.

use crate::abuse::{Ban, Net, BAN_MEMORY_SECS};
use rusqlite::{params, Connection, OptionalExtension};
use std::path::Path;

pub const SCHEMA_VERSION: i64 = 4;

const SCHEMA_V1: &str = "
CREATE TABLE serials (
    id          TEXT PRIMARY KEY,
    hash        TEXT NOT NULL UNIQUE,
    hint        TEXT NOT NULL,
    plan        TEXT NOT NULL,
    seats       INTEGER NOT NULL CHECK (seats > 0),
    status      TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    note        TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    revoked_at  INTEGER
);
CREATE TABLE activations (
    id            TEXT PRIMARY KEY,
    serial_id     TEXT NOT NULL REFERENCES serials(id),
    install_id    TEXT NOT NULL,
    install_key   TEXT NOT NULL,
    machine       TEXT NOT NULL DEFAULT '',
    app_version   TEXT NOT NULL DEFAULT '',
    activated_at  INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL,
    ended_at      INTEGER
);
-- An install holds at most one active activation.
CREATE UNIQUE INDEX activations_install_active ON activations(install_id) WHERE ended_at IS NULL;
CREATE INDEX activations_serial ON activations(serial_id, ended_at);
";

const SCHEMA_V2: &str = "
-- Client bans (auto: 5 failures in 15 min; admin: `ban add`). A row outlives its ban for
-- 30 days after the last ban so the next ban escalates.
CREATE TABLE bans (
    net           TEXT PRIMARY KEY,
    banned_until  INTEGER NOT NULL,
    ban_count     INTEGER NOT NULL,
    last_ban_at   INTEGER NOT NULL,
    last_code     TEXT NOT NULL DEFAULT '',
    source        TEXT NOT NULL CHECK (source IN ('auto', 'admin')),
    note          TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
-- Hourly outcome counters for `eggify-license stats`.
CREATE TABLE request_stats (
    hour   INTEGER NOT NULL,
    kind   TEXT NOT NULL,
    count  INTEGER NOT NULL,
    PRIMARY KEY (hour, kind)
);
";

/// The relay is a separate yearly subscription: the serial carries its end.
const SCHEMA_V3: &str = "ALTER TABLE serials ADD COLUMN relay_until INTEGER;";

/// "I'm interested" sign-ups from the website. One row per address (case-insensitive).
const SCHEMA_V4: &str = "
CREATE TABLE interests (
    id           TEXT PRIMARY KEY,
    email        TEXT NOT NULL,
    name         TEXT,
    note         TEXT,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    submissions  INTEGER NOT NULL DEFAULT 1,
    ip_hash      TEXT NOT NULL,
    user_agent   TEXT
);
CREATE UNIQUE INDEX interests_email ON interests(lower(email));
";

pub fn open(path: &Path) -> rusqlite::Result<Connection> {
    let conn = Connection::open(path)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600));
    }
    conn.busy_timeout(std::time::Duration::from_secs(5))?;
    conn.pragma_update(None, "journal_mode", "WAL")?;
    conn.pragma_update(None, "foreign_keys", "ON")?;
    conn.pragma_update(None, "synchronous", "NORMAL")?;
    migrate(&conn)?;
    Ok(conn)
}

fn migrate(conn: &Connection) -> rusqlite::Result<()> {
    conn.execute_batch("CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)")?;
    let current: i64 = conn.query_row(
        "SELECT COALESCE(MAX(version), 0) FROM schema_version",
        [],
        |r| r.get(0),
    )?;
    if current < 1 {
        let tx = conn.unchecked_transaction()?;
        tx.execute_batch(SCHEMA_V1)?;
        tx.execute("INSERT INTO schema_version (version) VALUES (1)", [])?;
        tx.commit()?;
    }
    if current < 2 {
        let tx = conn.unchecked_transaction()?;
        tx.execute_batch(SCHEMA_V2)?;
        tx.execute("INSERT INTO schema_version (version) VALUES (2)", [])?;
        tx.commit()?;
    }
    if current < 3 {
        let tx = conn.unchecked_transaction()?;
        tx.execute_batch(SCHEMA_V3)?;
        tx.execute("INSERT INTO schema_version (version) VALUES (3)", [])?;
        tx.commit()?;
    }
    if current < 4 {
        let tx = conn.unchecked_transaction()?;
        tx.execute_batch(SCHEMA_V4)?;
        tx.execute("INSERT INTO schema_version (version) VALUES (4)", [])?;
        tx.commit()?;
    }
    // Later migrations go here: `if current < 5 { ... }`.
    Ok(())
}

#[derive(Debug, Clone)]
pub struct Serial {
    pub id: String,
    pub hint: String,
    pub plan: String,
    pub seats: i64,
    pub status: String,
    pub note: String,
    pub created_at: i64,
    pub revoked_at: Option<i64>,
    /// End of the relay subscription (unix seconds); `None` = no relay.
    pub relay_until: Option<i64>,
}

#[derive(Debug, Clone)]
pub struct Activation {
    pub id: String,
    pub serial_id: String,
    pub install_id: String,
    pub machine: String,
    pub app_version: String,
    pub activated_at: i64,
    pub last_seen_at: i64,
    pub ended_at: Option<i64>,
}

const SERIAL_COLS: &str =
    "id, hint, plan, seats, status, note, created_at, revoked_at, relay_until";
const ACT_COLS: &str =
    "id, serial_id, install_id, machine, app_version, activated_at, last_seen_at, ended_at";

fn row_serial(r: &rusqlite::Row) -> rusqlite::Result<Serial> {
    Ok(Serial {
        id: r.get(0)?,
        hint: r.get(1)?,
        plan: r.get(2)?,
        seats: r.get(3)?,
        status: r.get(4)?,
        note: r.get(5)?,
        created_at: r.get(6)?,
        revoked_at: r.get(7)?,
        relay_until: r.get(8)?,
    })
}

fn row_activation(r: &rusqlite::Row) -> rusqlite::Result<Activation> {
    Ok(Activation {
        id: r.get(0)?,
        serial_id: r.get(1)?,
        install_id: r.get(2)?,
        machine: r.get(3)?,
        app_version: r.get(4)?,
        activated_at: r.get(5)?,
        last_seen_at: r.get(6)?,
        ended_at: r.get(7)?,
    })
}

#[allow(clippy::too_many_arguments)]
pub fn insert_serial(
    conn: &Connection,
    hash: &str,
    hint: &str,
    plan: &str,
    seats: i64,
    note: &str,
    relay_until: Option<i64>,
    now: i64,
) -> rusqlite::Result<String> {
    let id = uuid::Uuid::new_v4().to_string();
    conn.execute(
        "INSERT INTO serials (id, hash, hint, plan, seats, status, note, created_at, relay_until) VALUES (?1, ?2, ?3, ?4, ?5, 'active', ?6, ?7, ?8)",
        params![id, hash, hint, plan, seats, note, now, relay_until],
    )?;
    Ok(id)
}

pub fn serial_by_hash(conn: &Connection, hash: &str) -> rusqlite::Result<Option<Serial>> {
    conn.query_row(
        &format!("SELECT {SERIAL_COLS} FROM serials WHERE hash = ?1"),
        [hash],
        row_serial,
    )
    .optional()
}

pub fn serial_by_id(conn: &Connection, id: &str) -> rusqlite::Result<Option<Serial>> {
    conn.query_row(
        &format!("SELECT {SERIAL_COLS} FROM serials WHERE id = ?1"),
        [id],
        row_serial,
    )
    .optional()
}

pub fn list_serials(conn: &Connection) -> rusqlite::Result<Vec<(Serial, i64)>> {
    let mut st = conn.prepare(&format!(
        "SELECT {SERIAL_COLS}, (SELECT COUNT(*) FROM activations a WHERE a.serial_id = serials.id AND a.ended_at IS NULL) FROM serials ORDER BY created_at, id"
    ))?;
    let rows = st.query_map([], |r| Ok((row_serial(r)?, r.get(9)?)))?;
    rows.collect()
}

pub fn set_relay_until(
    conn: &Connection,
    id: &str,
    relay_until: Option<i64>,
) -> rusqlite::Result<bool> {
    let n = conn.execute(
        "UPDATE serials SET relay_until = ?2 WHERE id = ?1",
        params![id, relay_until],
    )?;
    Ok(n > 0)
}

pub fn revoke_serial(conn: &Connection, id: &str, now: i64) -> rusqlite::Result<bool> {
    let n = conn.execute(
        "UPDATE serials SET status = 'revoked', revoked_at = ?2 WHERE id = ?1 AND status = 'active'",
        params![id, now],
    )?;
    Ok(n > 0)
}

pub fn active_activations(conn: &Connection, serial_id: &str) -> rusqlite::Result<Vec<Activation>> {
    let mut st = conn.prepare(&format!(
        "SELECT {ACT_COLS} FROM activations WHERE serial_id = ?1 AND ended_at IS NULL ORDER BY activated_at, rowid"
    ))?;
    let rows = st.query_map([serial_id], row_activation)?;
    rows.collect()
}

pub fn all_activations(conn: &Connection, serial_id: &str) -> rusqlite::Result<Vec<Activation>> {
    let mut st = conn.prepare(&format!(
        "SELECT {ACT_COLS} FROM activations WHERE serial_id = ?1 ORDER BY activated_at, rowid"
    ))?;
    let rows = st.query_map([serial_id], row_activation)?;
    rows.collect()
}

pub fn active_activation_for_install(
    conn: &Connection,
    install_id: &str,
) -> rusqlite::Result<Option<Activation>> {
    conn.query_row(
        &format!("SELECT {ACT_COLS} FROM activations WHERE install_id = ?1 AND ended_at IS NULL"),
        [install_id],
        row_activation,
    )
    .optional()
}

pub fn end_activation(conn: &Connection, id: &str, now: i64) -> rusqlite::Result<bool> {
    let n = conn.execute(
        "UPDATE activations SET ended_at = ?2 WHERE id = ?1 AND ended_at IS NULL",
        params![id, now],
    )?;
    Ok(n > 0)
}

pub fn touch_activation(
    conn: &Connection,
    id: &str,
    machine: Option<&str>,
    app_version: Option<&str>,
    now: i64,
) -> rusqlite::Result<()> {
    conn.execute(
        "UPDATE activations SET last_seen_at = ?2, machine = COALESCE(?3, machine), app_version = COALESCE(?4, app_version) WHERE id = ?1",
        params![id, now, machine, app_version],
    )?;
    Ok(())
}

#[allow(clippy::too_many_arguments)]
pub fn insert_activation(
    conn: &Connection,
    serial_id: &str,
    install_id: &str,
    install_key: &str,
    machine: &str,
    app_version: &str,
    now: i64,
) -> rusqlite::Result<String> {
    let id = uuid::Uuid::new_v4().to_string();
    conn.execute(
        "INSERT INTO activations (id, serial_id, install_id, install_key, machine, app_version, activated_at, last_seen_at) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?7)",
        params![id, serial_id, install_id, install_key, machine, app_version, now],
    )?;
    Ok(id)
}

pub fn activation_by_id(conn: &Connection, id: &str) -> rusqlite::Result<Option<Activation>> {
    conn.query_row(
        &format!("SELECT {ACT_COLS} FROM activations WHERE id = ?1"),
        [id],
        row_activation,
    )
    .optional()
}

// ---------- interest ----------

/// One form submission, as stored on first contact (`upsert_interest` merges the repeats).
pub struct InterestSubmission<'a> {
    /// Already trimmed and lower-cased.
    pub email: &'a str,
    pub name: Option<&'a str>,
    pub note: Option<&'a str>,
    pub ip_hash: &'a str,
    pub user_agent: Option<&'a str>,
}

#[derive(Debug, Clone)]
pub struct Interest {
    pub email: String,
    pub name: Option<String>,
    pub note: Option<String>,
    pub created_at: i64,
    pub updated_at: i64,
    pub submissions: i64,
}

/// Stores a submission, or merges it into the row for the same address: the name and note are
/// kept unless the new ones are given, `updated_at` moves on and `submissions` goes up.
pub fn upsert_interest(
    conn: &Connection,
    s: &InterestSubmission,
    now: i64,
) -> rusqlite::Result<bool> {
    upsert_interest_capped(conn, s, now, INTEREST_NEW_PER_HOUR, INTEREST_MAX_ROWS)
}

/// New sign-ups accepted per hour across all clients, and rows kept in total. Generous for a
/// landing-page form; they stop a flood of made-up addresses from filling the disk.
pub const INTEREST_NEW_PER_HOUR: i64 = 500;
pub const INTEREST_MAX_ROWS: i64 = 100_000;

/// Like `upsert_interest` with explicit caps. A repeat address is always merged (it adds no
/// row); a new one is dropped silently (`Ok(false)`) once either cap is reached.
pub fn upsert_interest_capped(
    conn: &Connection,
    s: &InterestSubmission,
    now: i64,
    new_per_hour: i64,
    max_rows: i64,
) -> rusqlite::Result<bool> {
    let exists: bool = conn.query_row(
        "SELECT EXISTS(SELECT 1 FROM interests WHERE lower(email) = lower(?1))",
        [s.email],
        |r| r.get(0),
    )?;
    if !exists {
        let (total, recent): (i64, i64) = conn.query_row(
            "SELECT COUNT(*), COALESCE(SUM(created_at > ?1), 0) FROM interests",
            [now - 3600],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )?;
        if total >= max_rows || recent >= new_per_hour {
            return Ok(false);
        }
    }
    let id = uuid::Uuid::new_v4().to_string();
    conn.execute(
        "INSERT INTO interests (id, email, name, note, created_at, updated_at, submissions, ip_hash, user_agent)
         VALUES (?1, ?2, ?3, ?4, ?5, ?5, 1, ?6, ?7)
         ON CONFLICT(lower(email)) DO UPDATE SET
           name = COALESCE(excluded.name, interests.name),
           note = COALESCE(excluded.note, interests.note),
           updated_at = excluded.updated_at,
           submissions = interests.submissions + 1",
        params![id, s.email, s.name, s.note, now, s.ip_hash, s.user_agent],
    )?;
    Ok(true)
}

fn row_interest(r: &rusqlite::Row) -> rusqlite::Result<Interest> {
    Ok(Interest {
        email: r.get(0)?,
        name: r.get(1)?,
        note: r.get(2)?,
        created_at: r.get(3)?,
        updated_at: r.get(4)?,
        submissions: r.get(5)?,
    })
}

const INTEREST_COLS: &str = "email, name, note, created_at, updated_at, submissions";

/// The newest sign-ups first (by first submission), at most `limit`.
pub fn list_interests(conn: &Connection, limit: i64) -> rusqlite::Result<Vec<Interest>> {
    let mut st = conn.prepare(&format!(
        "SELECT {INTEREST_COLS} FROM interests ORDER BY created_at DESC, id LIMIT ?1"
    ))?;
    let rows = st.query_map([limit], row_interest)?;
    rows.collect()
}

/// Every sign-up, oldest first (for export).
pub fn export_interests(conn: &Connection) -> rusqlite::Result<Vec<Interest>> {
    let mut st = conn.prepare(&format!(
        "SELECT {INTEREST_COLS} FROM interests ORDER BY created_at, id"
    ))?;
    let rows = st.query_map([], row_interest)?;
    rows.collect()
}

// ---------- bans ----------

pub fn load_bans(conn: &Connection) -> rusqlite::Result<Vec<Ban>> {
    let mut st = conn.prepare(
        "SELECT net, banned_until, ban_count, last_ban_at, last_code, source, note FROM bans ORDER BY last_ban_at DESC",
    )?;
    let rows = st.query_map([], |r| {
        let net: String = r.get(0)?;
        Ok((
            net,
            r.get(1)?,
            r.get(2)?,
            r.get(3)?,
            r.get(4)?,
            r.get(5)?,
            r.get(6)?,
        ))
    })?;
    let mut out = Vec::new();
    for row in rows {
        let (net, banned_until, ban_count, last_ban_at, last_code, source, note) = row?;
        match Net::parse(&net) {
            Ok(net) => out.push(Ban {
                net,
                banned_until,
                ban_count,
                last_ban_at,
                last_code,
                source,
                note,
            }),
            Err(e) => eprintln!("ignoring ban row {net:?}: {e}"),
        }
    }
    Ok(out)
}

pub fn upsert_ban(conn: &Connection, b: &Ban, now: i64) -> rusqlite::Result<()> {
    conn.execute(
        "INSERT INTO bans (net, banned_until, ban_count, last_ban_at, last_code, source, note, created_at)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
         ON CONFLICT(net) DO UPDATE SET banned_until = excluded.banned_until, ban_count = excluded.ban_count,
           last_ban_at = excluded.last_ban_at, last_code = excluded.last_code, source = excluded.source,
           note = excluded.note",
        params![b.net.to_string(), b.banned_until, b.ban_count, b.last_ban_at, b.last_code, b.source, b.note, now],
    )?;
    Ok(())
}

pub fn delete_ban(conn: &Connection, net: &Net) -> rusqlite::Result<bool> {
    Ok(conn.execute("DELETE FROM bans WHERE net = ?1", [net.to_string()])? > 0)
}

/// Removes bans that ended and whose count is no longer remembered.
pub fn cleanup_bans(conn: &Connection, now: i64) -> rusqlite::Result<usize> {
    conn.execute(
        "DELETE FROM bans WHERE banned_until <= ?1 AND last_ban_at <= ?2",
        params![now, now - BAN_MEMORY_SECS],
    )
}

// ---------- stats ----------

pub fn add_stats(conn: &mut Connection, counters: &[(i64, String, i64)]) -> rusqlite::Result<()> {
    let tx = conn.transaction()?;
    for (hour, kind, n) in counters {
        tx.execute(
            "INSERT INTO request_stats (hour, kind, count) VALUES (?1, ?2, ?3)
             ON CONFLICT(hour, kind) DO UPDATE SET count = count + excluded.count",
            params![hour, kind, n],
        )?;
    }
    tx.commit()
}

/// Sums per kind since `since` (unix seconds; whole hours).
pub fn stats_since(conn: &Connection, since: i64) -> rusqlite::Result<Vec<(String, i64)>> {
    let mut st = conn.prepare(
        "SELECT kind, SUM(count) FROM request_stats WHERE hour >= ?1 GROUP BY kind ORDER BY kind",
    )?;
    let rows = st.query_map([since.div_euclid(3600)], |r| Ok((r.get(0)?, r.get(1)?)))?;
    rows.collect()
}

pub fn prune_stats(conn: &Connection, before: i64) -> rusqlite::Result<usize> {
    conn.execute(
        "DELETE FROM request_stats WHERE hour < ?1",
        [before.div_euclid(3600)],
    )
}

pub fn count_new_activations_since(conn: &Connection, since: i64) -> rusqlite::Result<i64> {
    conn.query_row(
        "SELECT COUNT(*) FROM activations WHERE activated_at >= ?1",
        [since],
        |r| r.get(0),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn migrates_once_and_reopens() {
        let dir = tempfile::tempdir().unwrap();
        let p = dir.path().join("l.db");
        {
            let c = open(&p).unwrap();
            insert_serial(&c, "h", "EGG-…-AAAAA", "standard", 3, "", None, 1).unwrap();
        }
        let c = open(&p).unwrap();
        let v: i64 = c
            .query_row("SELECT MAX(version) FROM schema_version", [], |r| r.get(0))
            .unwrap();
        assert_eq!(v, SCHEMA_VERSION);
        assert!(serial_by_hash(&c, "h").unwrap().is_some());
    }

    #[test]
    fn interest_upserts_by_address_and_keeps_name() {
        let dir = tempfile::tempdir().unwrap();
        let c = open(&dir.path().join("l.db")).unwrap();
        let first = InterestSubmission {
            email: "a@example.com",
            name: Some("Ada"),
            note: None,
            ip_hash: "h1",
            user_agent: None,
        };
        upsert_interest(&c, &first, 10).unwrap();
        let again = InterestSubmission {
            name: None,
            note: Some("hi"),
            ..first
        };
        upsert_interest(&c, &again, 20).unwrap();
        // Same address in another case is the same row.
        let other_case = InterestSubmission {
            email: "A@Example.com",
            ..again
        };
        upsert_interest(&c, &other_case, 30).unwrap();
        let rows = list_interests(&c, 50).unwrap();
        assert_eq!(rows.len(), 1);
        let r = &rows[0];
        assert_eq!(
            (r.name.as_deref(), r.note.as_deref(), r.submissions),
            (Some("Ada"), Some("hi"), 3)
        );
        assert_eq!((r.created_at, r.updated_at), (10, 30));
    }

    #[test]
    fn interest_caps_drop_new_addresses_but_still_merge_repeats() {
        let dir = tempfile::tempdir().unwrap();
        let c = open(&dir.path().join("l.db")).unwrap();
        let sub = |email| InterestSubmission {
            email,
            name: None,
            note: None,
            ip_hash: "h",
            user_agent: None,
        };
        // Hourly cap of 2 new rows.
        assert!(upsert_interest_capped(&c, &sub("a@x.com"), 100, 2, 10).unwrap());
        assert!(upsert_interest_capped(&c, &sub("b@x.com"), 200, 2, 10).unwrap());
        assert!(!upsert_interest_capped(&c, &sub("c@x.com"), 300, 2, 10).unwrap());
        // A repeat of a stored address still merges; an hour later new ones are accepted.
        assert!(upsert_interest_capped(&c, &sub("A@x.com"), 301, 2, 10).unwrap());
        assert!(upsert_interest_capped(&c, &sub("c@x.com"), 4000, 2, 10).unwrap());
        // Total cap of 3 rows.
        assert!(!upsert_interest_capped(&c, &sub("d@x.com"), 9000, 99, 3).unwrap());
        assert_eq!(list_interests(&c, 50).unwrap().len(), 3);
    }

    #[test]
    fn one_active_activation_per_install() {
        let dir = tempfile::tempdir().unwrap();
        let c = open(&dir.path().join("l.db")).unwrap();
        let s = insert_serial(&c, "h", "x", "standard", 3, "", None, 1).unwrap();
        let a = insert_activation(&c, &s, "inst", "k", "", "", 1).unwrap();
        assert!(insert_activation(&c, &s, "inst", "k", "", "", 2).is_err());
        end_activation(&c, &a, 3).unwrap();
        insert_activation(&c, &s, "inst", "k", "", "", 4).unwrap();
    }
}
