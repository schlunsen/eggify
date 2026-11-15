use clap::{Parser, Subcommand};
use eggify_license::{admin, api, crypto, db};
use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

const DEFAULT_DB: &str = "/var/lib/eggify-license/license.db";
const DEFAULT_KEY: &str = "/etc/eggify-license/signing.pem";

#[derive(Parser)]
#[command(
    name = "eggify-license",
    version,
    about = "Eggify license server and admin tool"
)]
struct Cli {
    #[command(subcommand)]
    cmd: Cmd,
}

#[derive(Subcommand)]
enum Cmd {
    /// Run the HTTP API.
    Serve {
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
        #[arg(long, default_value = DEFAULT_KEY)]
        key: PathBuf,
        #[arg(long, default_value = "127.0.0.1:8471")]
        listen: SocketAddr,
    },
    /// Create, list or revoke serials.
    Serial {
        #[command(subcommand)]
        cmd: SerialCmd,
    },
    /// List the activations of a serial (by serial number or id).
    Activations {
        serial: String,
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// End one activation, freeing its seat.
    Deactivate {
        activation_id: String,
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// List, add or remove client bans (a running server picks changes up within 30 s).
    Ban {
        #[command(subcommand)]
        cmd: BanCmd,
    },
    /// List or export the "I'm interested" sign-ups from the website.
    Interest {
        #[command(subcommand)]
        cmd: InterestCmd,
    },
    /// Activations, renewals, failures and bans in the last 24 hours.
    Stats {
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// Write a new Ed25519 signing key (PKCS#8 PEM, mode 0600). Never overwrites.
    Keygen {
        #[arg(long)]
        out: PathBuf,
    },
    /// Print a signing key's raw public key (standard base64).
    Pubkey {
        #[arg(long, default_value = DEFAULT_KEY)]
        key: PathBuf,
    },
}

#[derive(Subcommand)]
enum InterestCmd {
    /// List the newest sign-ups.
    List {
        #[arg(long, default_value_t = 50)]
        limit: i64,
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// Print every sign-up as CSV on stdout (oldest first).
    Export {
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
}

#[derive(Subcommand)]
enum BanCmd {
    /// List bans (active, and ended ones whose count is still remembered).
    List {
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// Ban an IP (IPv4, or the IPv6 /64 it is in) or a CIDR block.
    Add {
        net: String,
        #[arg(long)]
        hours: f64,
        #[arg(long, default_value = "")]
        note: String,
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// Lift a ban and forget its count.
    Remove {
        net: String,
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
}

#[derive(Subcommand)]
enum SerialCmd {
    /// Create a serial and print it once (only its hash is stored).
    Create {
        #[arg(long, default_value_t = 3)]
        seats: i64,
        #[arg(long, default_value = "standard")]
        plan: String,
        #[arg(long, default_value = "")]
        note: String,
        /// Include this many months of the relay subscription (0 = none).
        #[arg(long, default_value_t = 0)]
        relay_months: u32,
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// Change a serial's relay subscription (by serial number or id).
    Relay {
        serial: String,
        /// Add N months to the current end (or to now, if it has ended or there is none).
        #[arg(long, group = "change")]
        extend_months: Option<u32>,
        /// Relay works through this date (UTC), YYYY-MM-DD.
        #[arg(long, group = "change")]
        until: Option<String>,
        /// No relay.
        #[arg(long, group = "change")]
        off: bool,
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// List serials.
    List {
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
    /// Revoke a serial (by serial number or id).
    Revoke {
        serial: String,
        #[arg(long, default_value = DEFAULT_DB)]
        db: PathBuf,
    },
}

fn relay_change(
    extend_months: Option<u32>,
    until: Option<String>,
    off: bool,
) -> Result<admin::RelayChange, String> {
    match (extend_months, until, off) {
        (Some(n), None, false) => Ok(admin::RelayChange::ExtendMonths(n)),
        (None, Some(d), false) => admin::parse_until_date(&d).map(admin::RelayChange::Until),
        (None, None, true) => Ok(admin::RelayChange::Off),
        _ => Err("give exactly one of --extend-months N, --until YYYY-MM-DD, --off".into()),
    }
}

fn open_db(path: &Path) -> Result<rusqlite::Connection, String> {
    db::open(path).map_err(|e| format!("open {}: {e}", path.display()))
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    let res = match cli.cmd {
        Cmd::Serve { db, key, listen } => serve(db, key, listen),
        Cmd::Serial {
            cmd:
                SerialCmd::Create {
                    seats,
                    plan,
                    note,
                    relay_months,
                    db,
                },
        } => open_db(&db)
            .and_then(|c| admin::cmd_serial_create(&c, seats, &plan, &note, relay_months)),
        Cmd::Serial {
            cmd:
                SerialCmd::Relay {
                    serial,
                    extend_months,
                    until,
                    off,
                    db,
                },
        } => relay_change(extend_months, until, off).and_then(|change| {
            open_db(&db).and_then(|c| admin::cmd_serial_relay(&c, &serial, change))
        }),
        Cmd::Serial {
            cmd: SerialCmd::List { db },
        } => open_db(&db).and_then(|c| admin::cmd_serial_list(&c)),
        Cmd::Serial {
            cmd: SerialCmd::Revoke { serial, db },
        } => open_db(&db).and_then(|c| admin::cmd_serial_revoke(&c, &serial)),
        Cmd::Activations { serial, db } => {
            open_db(&db).and_then(|c| admin::cmd_activations(&c, &serial))
        }
        Cmd::Deactivate { activation_id, db } => {
            open_db(&db).and_then(|c| admin::cmd_deactivate(&c, &activation_id))
        }
        Cmd::Ban {
            cmd: BanCmd::List { db },
        } => open_db(&db).and_then(|c| admin::cmd_ban_list(&c)),
        Cmd::Ban {
            cmd:
                BanCmd::Add {
                    net,
                    hours,
                    note,
                    db,
                },
        } => open_db(&db).and_then(|c| admin::cmd_ban_add(&c, &net, hours, &note)),
        Cmd::Ban {
            cmd: BanCmd::Remove { net, db },
        } => open_db(&db).and_then(|c| admin::cmd_ban_remove(&c, &net)),
        Cmd::Interest {
            cmd: InterestCmd::List { limit, db },
        } => open_db(&db).and_then(|c| admin::cmd_interest_list(&c, limit)),
        Cmd::Interest {
            cmd: InterestCmd::Export { db },
        } => open_db(&db).and_then(|c| admin::cmd_interest_export(&c)),
        Cmd::Stats { db } => open_db(&db).and_then(|c| admin::cmd_stats(&c)),
        Cmd::Keygen { out } => crypto::write_new_signing_key(&out).map(|k| {
            println!("wrote {}", out.display());
            println!(
                "public key: {}",
                crypto::public_key_std_b64(&k.verifying_key())
            );
        }),
        Cmd::Pubkey { key } => crypto::load_signing_key(&key)
            .map(|k| println!("{}", crypto::public_key_std_b64(&k.verifying_key()))),
    };
    match res {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("error: {e}");
            ExitCode::FAILURE
        }
    }
}

fn serve(db_path: PathBuf, key_path: PathBuf, listen: SocketAddr) -> Result<(), String> {
    let key = crypto::load_signing_key(&key_path)?;
    let conn = open_db(&db_path)?;
    let rt = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .enable_all()
        .build()
        .map_err(|e| e.to_string())?;
    rt.block_on(async move {
        // The website that may post the interest form from a browser, e.g. https://example.com.
        let site = std::env::var("EGGIFY_LICENSE_INTEREST_ORIGIN").ok();
        let state = api::AppState::new(conn, key.clone()).with_interest_origin(site.as_deref());
        let housekeeper = state.clone();
        tokio::spawn(async move {
            let mut t = tokio::time::interval(std::time::Duration::from_secs(30));
            t.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
            loop {
                t.tick().await;
                housekeeper.maintenance().await;
            }
        });
        let listener = tokio::net::TcpListener::bind(listen)
            .await
            .map_err(|e| format!("listen {listen}: {e}"))?;
        println!(
            "eggify-license {} listening on {listen} (public key {})",
            env!("CARGO_PKG_VERSION"),
            crypto::public_key_std_b64(&key.verifying_key())
        );
        axum::serve(
            listener,
            api::router(state.clone()).into_make_service_with_connect_info::<SocketAddr>(),
        )
        .with_graceful_shutdown(shutdown_signal())
        .await
        .map_err(|e| e.to_string())?;
        state.flush().await;
        println!("shut down");
        Ok(())
    })
}

async fn shutdown_signal() {
    let ctrl_c = async {
        let _ = tokio::signal::ctrl_c().await;
    };
    #[cfg(unix)]
    let term = async {
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(mut s) => {
                s.recv().await;
            }
            Err(_) => std::future::pending::<()>().await,
        }
    };
    #[cfg(not(unix))]
    let term = std::future::pending::<()>();
    tokio::select! {
        _ = ctrl_c => {},
        _ = term => {},
    }
    println!("shutting down");
}
