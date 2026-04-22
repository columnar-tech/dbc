use serde::{Deserialize, Serialize};
use std::time::Duration;
use tauri::AppHandle;

use crate::error::SidecarError;
use crate::sidecar::Sidecar;

#[derive(Debug, Deserialize, Serialize)]
pub struct SearchDriver {
    pub driver: String,
    pub description: String,
    pub installed: Option<Vec<String>>,
    pub registry: Option<String>,
    pub license: Option<String>,
    pub installed_versions: Option<std::collections::HashMap<String, Vec<String>>>,
    pub available_versions: Option<Vec<String>>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct SearchResponse {
    pub drivers: Vec<SearchDriver>,
    pub warning: Option<String>,
}

#[derive(Debug, Deserialize)]
struct Envelope<T> {
    payload: T,
}

#[tauri::command]
pub async fn search_drivers(
    app: AppHandle,
    query: Option<String>,
    include_prerelease: bool,
    verbose: bool,
) -> Result<SearchResponse, SidecarError> {
    let sidecar = Sidecar::new(app);
    let mut args = vec!["search"];

    let query_string = query.as_deref().filter(|q| !q.is_empty());
    if let Some(q) = query_string {
        args.push(q);
    }

    if verbose {
        args.push("-v");
    }
    if include_prerelease {
        args.push("--pre");
    }

    let envelope: Envelope<SearchResponse> = sidecar
        .run_json(&args, Duration::from_secs(30))
        .await?;

    Ok(envelope.payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_search_driver_serde_roundtrip() {
        let driver = SearchDriver {
            driver: "snowflake".to_string(),
            description: "Snowflake ADBC driver".to_string(),
            installed: None,
            registry: Some("https://example.com".to_string()),
            license: Some("Apache-2.0".to_string()),
            installed_versions: None,
            available_versions: Some(vec!["1.0.0".to_string()]),
        };
        let json = serde_json::to_string(&driver).unwrap();
        let parsed: SearchDriver = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.driver, "snowflake");
        assert_eq!(parsed.description, "Snowflake ADBC driver");
        assert!(parsed.installed.is_none());
    }

    #[test]
    fn test_search_response_empty() {
        let response = SearchResponse {
            drivers: vec![],
            warning: None,
        };
        let json = serde_json::to_string(&response).unwrap();
        let parsed: SearchResponse = serde_json::from_str(&json).unwrap();
        assert!(parsed.drivers.is_empty());
        assert!(parsed.warning.is_none());
    }

    #[test]
    fn test_envelope_ignores_extra_fields() {
        let json = r#"{"schema_version":1,"kind":"search.results","payload":{"drivers":[],"warning":null}}"#;
        let env: Envelope<SearchResponse> = serde_json::from_str(json).unwrap();
        assert!(env.payload.drivers.is_empty());
        assert!(env.payload.warning.is_none());
    }
}
