pub mod cmds;
pub mod error;
pub mod sidecar;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_opener::init())
        .invoke_handler(tauri::generate_handler![
            cmds::search::search_drivers,
            cmds::info::get_driver_info,
            cmds::installed::list_installed,
            cmds::uninstall::uninstall_driver,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application")
}
