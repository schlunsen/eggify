//! Serial numbers: `EGG-XXXXX-XXXXX-XXXXX-XXXXX` (see docs/licensing-protocol.md, "Serial numbers").

use rand::RngCore;
use sha2::{Digest, Sha256};

/// Crockford base32 alphabet; a character's value is its index.
pub const ALPHABET: &[u8; 32] = b"0123456789ABCDEFGHJKMNPQRSTVWXYZ";
/// Random characters before the check character.
pub const BODY_LEN: usize = 19;
/// Characters in a normalised serial (body + check character).
pub const SERIAL_LEN: usize = 20;

fn value(c: u8) -> Option<usize> {
    ALPHABET.iter().position(|&a| a == c)
}

/// Check character for a 19-character body: `ALPHABET[(Σ i·value(c_i)) mod 31]`, i 1-based.
/// Returns `None` when the body has the wrong length or a character outside the alphabet.
pub fn check_char(body: &str) -> Option<char> {
    let bytes = body.as_bytes();
    if bytes.len() != BODY_LEN {
        return None;
    }
    let mut sum = 0usize;
    for (i, &c) in bytes.iter().enumerate() {
        sum += (i + 1) * value(c)?;
    }
    Some(ALPHABET[sum % 31] as char)
}

/// Normalises user input: upper-case; drop spaces, `-` and `_`; drop a leading `EGG` unless
/// exactly 20 characters are left; map `O`→`0`, `I`/`L`→`1`. Returns the 20 characters only
/// when they are all in the alphabet and the check character is right; anything else is
/// `invalid_serial`.
pub fn normalise(input: &str) -> Option<String> {
    let mut s: String = input
        .chars()
        .filter(|c| !matches!(c, ' ' | '-' | '_'))
        .flat_map(char::to_uppercase)
        .collect();
    if s.len() > SERIAL_LEN {
        if let Some(rest) = s.strip_prefix("EGG") {
            s = rest.to_string();
        }
    }
    let s: String = s
        .chars()
        .map(|c| match c {
            'O' => '0',
            'I' | 'L' => '1',
            c => c,
        })
        .collect();
    if s.len() != SERIAL_LEN || !s.bytes().all(|b| value(b).is_some()) {
        return None;
    }
    let (body, check) = s.split_at(BODY_LEN);
    if check_char(body)? != check.chars().next()? {
        return None;
    }
    Some(s)
}

/// Formats 20 normalised characters as `EGG-XXXXX-XXXXX-XXXXX-XXXXX`.
pub fn format(normalised: &str) -> String {
    let groups: Vec<&str> = (0..4).map(|g| &normalised[g * 5..g * 5 + 5]).collect();
    format!("EGG-{}", groups.join("-"))
}

/// Builds the full serial (formatted) for a 19-character body.
pub fn from_body(body: &str) -> Option<String> {
    let c = check_char(body)?;
    Some(format(&format!("{body}{c}")))
}

/// Display hint stored next to the hash: `EGG-…-<last group>`.
pub fn hint(normalised: &str) -> String {
    format!("EGG-…-{}", &normalised[15..20])
}

/// What the server stores: hex sha256 of the normalised 20 characters.
pub fn hash(normalised: &str) -> String {
    hex::encode(Sha256::digest(normalised.as_bytes()))
}

/// A new random serial, returned normalised (20 characters).
pub fn generate() -> String {
    let mut bytes = [0u8; BODY_LEN];
    rand::rngs::OsRng.fill_bytes(&mut bytes);
    // 32 is a power of two, so `& 31` picks every character with equal probability.
    let body: String = bytes
        .iter()
        .map(|b| ALPHABET[(b & 31) as usize] as char)
        .collect();
    let c = check_char(&body).expect("body is valid by construction");
    format!("{body}{c}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn spec_vectors() {
        let vectors = [
            ("0000000000000000000", "EGG-00000-00000-00000-00000"),
            ("ABCDEFGHJKMNPQRSTVW", "EGG-ABCDE-FGHJK-MNPQR-STVWT"),
            ("ZZZZZZZZZZZZZZZZZZZ", "EGG-ZZZZZ-ZZZZZ-ZZZZZ-ZZZZ0"),
            ("7H3K9Q2M4X8R6T1V5N0", "EGG-7H3K9-Q2M4X-8R6T1-V5N0T"),
        ];
        for (body, serial) in vectors {
            assert_eq!(from_body(body).as_deref(), Some(serial), "body {body}");
            assert!(normalise(serial).is_some(), "{serial} validates");
        }
    }

    #[test]
    fn spec_normalisation_example() {
        assert_eq!(
            normalise("egg 7h3k9-q2m4x-8r6t1-v5n0t").as_deref(),
            Some("7H3K9Q2M4X8R6T1V5N0T")
        );
        // O→0, I/L→1, underscores, no EGG prefix.
        assert_eq!(
            normalise("7h3k9_q2m4x_8r6tl_v5nOt").as_deref(),
            Some("7H3K9Q2M4X8R6T1V5N0T")
        );
        assert_eq!(
            normalise("7H3K9Q2M4X8R6TIV5N0T").as_deref(),
            Some("7H3K9Q2M4X8R6T1V5N0T")
        );
    }

    #[test]
    fn wrong_check_character_is_rejected() {
        assert_eq!(normalise("EGG-7H3K9-Q2M4X-8R6T1-V5N0A"), None);
    }

    #[test]
    fn serial_starting_with_egg_is_not_mistaken_for_the_prefix() {
        let serial = "EGG00000000000000001";
        assert_eq!(normalise("EGG-EGG00-00000-00000-00001").as_deref(), Some(serial));
        assert_eq!(normalise(serial).as_deref(), Some(serial));
        assert_eq!(normalise("EGGEGG00000000000000001").as_deref(), Some(serial));
    }

    #[test]
    fn malformed_input_is_rejected() {
        for bad in [
            "",
            "EGG",
            "EGG-7H3K9-Q2M4X-8R6T1-V5N0",
            "EGG-7H3K9-Q2M4X-8R6T1-V5N0TT",
            "EGG-7H3K9-Q2M4X-8R6T1-V5NUT",
            "7H3K9Q2M4X8R6T1V5N0T!",
        ] {
            assert_eq!(normalise(bad), None, "{bad:?}");
        }
    }

    #[test]
    fn single_character_changes_are_caught_except_0_and_z() {
        // Values are taken mod 31, and `0` (0) and `Z` (31) are congruent, so swapping
        // one for the other in the body is the one single-character change the check
        // character cannot see. Every other substitution is caught.
        let good = "7H3K9Q2M4X8R6T1V5N0T";
        for pos in 0..SERIAL_LEN {
            for &c in ALPHABET.iter() {
                let mut b = good.as_bytes().to_vec();
                if b[pos] == c {
                    continue;
                }
                let blind = pos < BODY_LEN && matches!((b[pos], c), (b'0', b'Z') | (b'Z', b'0'));
                b[pos] = c;
                let s = String::from_utf8(b).unwrap();
                assert_eq!(normalise(&s).is_some(), blind, "{s}");
            }
        }
    }

    #[test]
    fn generated_serials_always_validate() {
        for _ in 0..2000 {
            let s = generate();
            assert_eq!(s.len(), SERIAL_LEN);
            assert_eq!(normalise(&s).as_deref(), Some(s.as_str()));
            assert_eq!(normalise(&format(&s)).as_deref(), Some(s.as_str()));
        }
    }

    #[test]
    fn hint_and_hash() {
        assert_eq!(hint("7H3K9Q2M4X8R6T1V5N0T"), "EGG-…-V5N0T");
        assert_eq!(hash("7H3K9Q2M4X8R6T1V5N0T").len(), 64);
    }
}
