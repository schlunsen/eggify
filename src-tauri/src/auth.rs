//! Credentials for the desktop app's own calls to the Eggify sidecar.
//!
//! The sidecar requires auth on every /api/ request, so these calls send the
//! server's API key, which it stores in `~/.claude/eggify/.secret` (0600).

use std::path::PathBuf;

fn secret_path() -> Option<PathBuf> {
    let home = std::env::var_os("HOME")?;
    Some(
        PathBuf::from(home)
            .join(".claude")
            .join("eggify")
            .join(".secret"),
    )
}

/// Read the sidecar's API key. Re-read on every call: the file is created by
/// the sidecar on first start and may be regenerated.
pub fn api_key() -> Option<String> {
    let raw = std::fs::read_to_string(secret_path()?).ok()?;
    let key = raw.trim();
    if key.is_empty() {
        None
    } else {
        Some(key.to_string())
    }
}

/// Attach `Authorization: Bearer <api key>` when the key is available.
pub fn with_auth(req: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
    match api_key() {
        Some(key) => req.bearer_auth(key),
        None => req,
    }
}
