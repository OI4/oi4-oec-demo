//! Topics of the config service: the `Set Config` topic of a Config
//! DataSet and the MQTT match against the entries of a SubscriptionList
//! (SF-107: a Config is writable if the SubscriptionList lists its `Set`
//! topic).

/// The topic level of an Identification Link: base64url without padding
/// (REQ-TOP-018).
pub(crate) fn il_level(il: &str) -> String {
    const ALPHABET: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    let bytes = il.as_bytes();
    let mut out = String::with_capacity(bytes.len().div_ceil(3) * 4);
    for chunk in bytes.chunks(3) {
        let b = [
            chunk[0],
            chunk.get(1).copied().unwrap_or(0),
            chunk.get(2).copied().unwrap_or(0),
        ];
        let n = (u32::from(b[0]) << 16) | (u32::from(b[1]) << 8) | u32::from(b[2]);
        for i in 0..=chunk.len() {
            out.push(char::from(ALPHABET[(n >> (18 - 6 * i)) as usize & 63]));
        }
    }
    out
}

/// A Filter value as topic level: `%`, `+`, `#` and `/` percent-encoded,
/// nothing else (REQ-TOP-019).
pub(crate) fn filter_level(value: &str) -> String {
    let mut out = String::with_capacity(value.len());
    for c in value.chars() {
        match c {
            '%' => out.push_str("%25"),
            '+' => out.push_str("%2B"),
            '#' => out.push_str("%23"),
            '/' => out.push_str("%2F"),
            c => out.push(c),
        }
    }
    out
}

/// The topic of a `Set Config` of the context of `source` at the
/// application `service_type`/`app` (REQ-RES-CONFIG-015).
pub(crate) fn set_config(service_type: &str, app: &str, source: &str, context: &str) -> String {
    format!(
        "Oi4v2/{service_type}/{}/Set/Config/{}/{}",
        il_level(app),
        il_level(source),
        filter_level(context)
    )
}

/// Whether the MQTT topic filter `filter` (with `+` and `#`) matches the
/// topic `topic` (MQTT 3.1.1 §4.7).
pub(crate) fn matches(filter: &str, topic: &str) -> bool {
    let mut topic_levels = topic.split('/');
    for f in filter.split('/') {
        if f == "#" {
            return true;
        }
        match topic_levels.next() {
            Some(t) if f == "+" || f == t => {}
            _ => return false,
        }
    }
    topic_levels.next().is_none()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn identification_links_are_base64url_without_padding() {
        // The same as Go's base64.RawURLEncoding (oec/demos/internal/demotest).
        assert_eq!(
            il_level("oi4.org/OEC,20Logger/OEC-LOGGER/logger-1"),
            "b2k0Lm9yZy9PRUMsMjBMb2dnZXIvT0VDLUxPR0dFUi9sb2dnZXItMQ"
        );
        assert_eq!(il_level(""), "");
        assert_eq!(il_level("a"), "YQ");
        assert_eq!(il_level("ab"), "YWI");
        assert_eq!(il_level("abc"), "YWJj");
        assert_eq!(il_level("ÿþ?>"), "w7_Dvj8-");
    }

    #[test]
    fn filter_values_are_topic_encoded() {
        assert_eq!(filter_level("net/a+b#c%d e"), "net%2Fa%2Bb%23c%25d e");
        assert_eq!(filter_level("weather"), "weather");
    }

    #[test]
    fn set_config_topic() {
        assert_eq!(
            set_config("Utility", "a", "ab", "x/y"),
            "Oi4v2/Utility/YQ/Set/Config/YWI/x%2Fy"
        );
    }

    #[test]
    fn mqtt_match() {
        let t = "Oi4v2/Utility/YQ/Set/Config/YWI/logger";
        for (f, want) in [
            (t, true),
            ("Oi4v2/Utility/YQ/Set/Config/YWI/+", true),
            ("Oi4v2/Utility/YQ/Set/Config/#", true),
            ("Oi4v2/+/YQ/Set/+/YWI/logger", true),
            ("#", true),
            ("Oi4v2/Utility/YQ/Set/Config/YWI", false),
            ("Oi4v2/Utility/YQ/Set/Config/YWI/logger/x", false),
            ("Oi4v2/Utility/YQ/Set/Config/YWI/other", false),
            ("Oi4v2/Utility/YQ/Get/Config/#", false),
        ] {
            assert_eq!(matches(f, t), want, "{f}");
        }
    }
}
