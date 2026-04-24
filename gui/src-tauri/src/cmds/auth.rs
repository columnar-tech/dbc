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
    registry_url: String,
    purge: bool,
) -> Result<AuthLogoutResponse, SidecarError> {
    let registry_clone = registry_url.clone();
    let mut args_owned = vec!["auth".to_string(), "logout".to_string(), registry_url];
    if purge {
        args_owned.push("--purge".to_string());
    }
    let args: Vec<&str> = args_owned.iter().map(|s| s.as_str()).collect();
    let sidecar = Sidecar::new(app);
    match sidecar
        .run_json::<serde_json::Value>(&args, Duration::from_secs(10))
        .await
    {
        Ok(_) | Err(SidecarError::ParseError(_)) => Ok(AuthLogoutResponse {
            status: "success".to_string(),
            registry: registry_clone,
        }),
        Err(e) => Err(e),
    }
}

#[tauri::command]
pub async fn auth_status(_app: AppHandle) -> Result<AuthStatus, SidecarError> {
    Ok(AuthStatus {
        registries: vec![],
    })
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
