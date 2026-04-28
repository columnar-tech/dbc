pub mod cmds;
pub mod error;
pub mod sidecar;
pub mod state;

use state::AppState;
use tauri::Manager;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(AppState::new())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_opener::init())
        .invoke_handler(tauri::generate_handler![
            cmds::search::search_drivers,
            cmds::info::get_driver_info,
            cmds::installed::list_installed,
            cmds::uninstall::uninstall_driver,
            cmds::install::install_driver,
            cmds::install::install_driver_local,
            cmds::driver_list::load_driver_list,
            cmds::driver_list::init_driver_list,
            cmds::driver_list::add_driver,
            cmds::driver_list::remove_driver,
            cmds::driver_list::sync_drivers,
            cmds::auth::auth_login_device,
            cmds::auth::auth_login_apikey,
            cmds::auth::auth_logout,
            cmds::auth::auth_status,
            cmds::logs::get_logs,
            cmds::logs::clear_logs,
        ])
        .setup(|_app| {
            #[cfg(debug_assertions)]
            if let Some(window) = _app.get_webview_window("main") {
                window.open_devtools();
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application")
}
