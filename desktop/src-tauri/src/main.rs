// Keeps the console window from spawning a terminal behind it on Windows.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    promview_desktop_lib::run()
}
