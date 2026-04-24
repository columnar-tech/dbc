use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter};
use tokio::sync::oneshot;

use crate::error::SidecarError;
use crate::sidecar::Sidecar;

#[derive(Debug, Deserialize, Serialize)]
pub struct AuthLoginResponse {
    pub status: String,
    pub registry: String,
    pub message: Option<String>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct AuthLogoutResponse {
    pub status: String,
    pub registry: String,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct AuthRegistryStatus {
    pub url: String,
    pub authenticated: bool,
    pub auth_type: Option<String>,
    pub license_valid: bool,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct AuthStatus {
    pub registries: Vec<AuthRegistryStatus>,
}

#[tauri::command]
pub async fn auth_login_device(
    app: AppHandle,
    registry_url: String,
    job_id: String,
) -> Result<AuthLoginResponse, SidecarError> {
    let app_clone = app.clone();
    let job_id_clone = job_id.clone();
    let registry_clone = registry_url.clone();
    let (_cancel_tx, cancel_rx) = oneshot::channel::<()>();

    let sidecar = Sidecar::new(app);
    sidecar
        .run_stream::<serde_json::Value, _>(
            &["auth", "login", &registry_url],
            move |event| {
                let event_name = format!("auth-device-code:{}", job_id_clone);
                let _ = app_clone.emit(&event_name, &event);
            },
            cancel_rx,
            Duration::from_secs(300),
        )
        .await?;

    Ok(AuthLoginResponse {
        status: "success".to_string(),
        registry: registry_clone,
        message: None,
    })
}

#[tauri::command]
pub async fn auth_login_apikey(
    app: AppHandle,
    registry_url: String,
    api_key: String,
) -> Result<AuthLoginResponse, SidecarError> {
    let registry_clone = registry_url.clone();
    let sidecar = Sidecar::new(app);
    match sidecar
        .run_json::<serde_json::Value>(
            &["auth", "login", &registry_url, "--api-key", &api_key],
            Duration::from_secs(30),
        )
        .await
    {
        Ok(_) | Err(SidecarError::ParseError(_)) => Ok(AuthLoginResponse {
            status: "success".to_string(),
            registry: registry_clone,
            message: None,
        }),
        Err(e) => Err(e),
    }
}

#[tauri::command]
pub async fn auth_logout(
    app: AppHandle,
    registry_url: Option<String>,
    purge: bool,
) -> Result<AuthLogoutResponse, SidecarError> {
    let mut args_owned = vec!["auth".to_string(), "logout".to_string()];
    if let Some(ref url) = registry_url {
        args_owned.push(url.clone());
    }
    if purge {
        args_owned.push("--purge".to_string());
    }
    let registry = registry_url.unwrap_or_default();
    let args: Vec<&str> = args_owned.iter().map(|s| s.as_str()).collect();
    let sidecar = Sidecar::new(app);
    sidecar.run_plain(&args, Duration::from_secs(10)).await?;
    Ok(AuthLogoutResponse {
        status: "success".to_string(),
        registry,
    })
}

#[tauri::command]
pub async fn auth_status(_app: AppHandle) -> Result<AuthStatus, SidecarError> {
    let path = match get_credentials_path() {
        Ok(Some(p)) => p,
        Ok(None) => return Ok(AuthStatus { registries: vec![] }),
        Err(e) => return Err(e),
    };

    let content = match std::fs::read_to_string(&path) {
        Ok(c) => c,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            return Ok(AuthStatus { registries: vec![] });
        }
        Err(e) => return Err(SidecarError::Io(e.to_string())),
    };

    #[derive(serde::Deserialize)]
    struct CredEntry {
        #[serde(rename = "type")]
        cred_type: Option<String>,
        registry_url: Option<String>,
        token: Option<String>,
        api_key: Option<String>,
    }

    #[derive(serde::Deserialize)]
    struct CredsFile {
        #[serde(default)]
        credentials: Vec<CredEntry>,
    }

    let parsed: CredsFile = toml::from_str(&content)
        .map_err(|e| SidecarError::ParseError(e.to_string()))?;

    let registries = parsed
        .credentials
        .into_iter()
        .filter_map(|c| {
            let url = c.registry_url?;
            let authenticated = c.token.as_deref().map(|t| !t.is_empty()).unwrap_or(false)
                || c.api_key.as_deref().map(|k| !k.is_empty()).unwrap_or(false);
            Some(AuthRegistryStatus {
                url,
                authenticated,
                auth_type: c.cred_type,
                license_valid: false,
            })
        })
        .collect();

    Ok(AuthStatus { registries })
}

fn get_credentials_path() -> Result<Option<std::path::PathBuf>, SidecarError> {
    if let Ok(xdg) = std::env::var("XDG_DATA_HOME") {
        if !std::path::Path::new(&xdg).is_absolute() {
            return Err(SidecarError::Io(
                "XDG_DATA_HOME is set to a relative path, which is not allowed".to_string(),
            ));
        }
        return Ok(Some(
            std::path::PathBuf::from(xdg)
                .join("dbc")
                .join("credentials")
                .join("credentials.toml"),
        ));
    }

    #[cfg(target_os = "macos")]
    {
        Ok(dirs::config_dir().map(|d| {
            d.join("Columnar")
                .join("dbc")
                .join("credentials")
                .join("credentials.toml")
        }))
    }
    #[cfg(target_os = "linux")]
    {
        Ok(dirs::data_local_dir()
            .map(|d| d.join("dbc").join("credentials").join("credentials.toml")))
    }
    #[cfg(target_os = "windows")]
    {
        Ok(dirs::data_local_dir()
            .map(|d| d.join("dbc").join("credentials").join("credentials.toml")))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_auth_login_response_serializes() {
        let r = AuthLoginResponse {
            status: "success".to_string(),
            registry: "https://registry.example.com".to_string(),
            message: None,
        };
        let json = serde_json::to_string(&r).unwrap();
        assert!(json.contains("success"));
        assert!(json.contains("registry.example.com"));
    }

    #[test]
    fn test_auth_logout_response_serializes() {
        let r = AuthLogoutResponse {
            status: "success".to_string(),
            registry: "https://registry.example.com".to_string(),
        };
        let json = serde_json::to_string(&r).unwrap();
        assert!(json.contains("success"));
    }

    #[test]
    fn test_auth_status_empty() {
        let s = AuthStatus { registries: vec![] };
        let json = serde_json::to_string(&s).unwrap();
        assert!(json.contains("registries"));
    }

    #[test]
    fn test_auth_registry_status_serializes() {
        let r = AuthRegistryStatus {
            url: "https://registry.example.com".to_string(),
            authenticated: true,
            auth_type: Some("device_code".to_string()),
            license_valid: true,
        };
        let json = serde_json::to_string(&r).unwrap();
        assert!(json.contains("device_code"));
        assert!(json.contains("true"));
    }
}
