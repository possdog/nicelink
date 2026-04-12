# OpenCode Instructions: tllc

## Architecture & Tech Stack
- Minimal Rust link shortener using `axum` and `redb` (embedded database).
- `link_shortener.redb` is created/read in the current working directory.
- `index.html` is embedded into the binary at compile time via `include_str!`.

## Local Development & Running
- **Ports (Gotcha):** The application hardcodes bindings to ports `80` (HTTP) and `443` (HTTPS). Running locally (`cargo run`) requires root privileges (e.g., `sudo -E cargo run`) to bind to these privileged ports.
- **HTTPS:** The server expects `domain.cert.pem` and `private.key.pem` in the current working directory. If missing, it quietly skips HTTPS and only serves HTTP on port 80.
- **Formatting:** A custom `rustfmt.toml` dictates import grouping. Always run `cargo fmt` to adhere to the project's exact formatting conventions.

## Building for Deployment
- The project is deployed on a Debian server but typically developed on macOS.
- **DO NOT** just run `cargo build --release` for deployment.
- **MUST USE:** To build the release binary for Linux, execute the provided script: `./build-linux.sh`. This configures the correct cross-compilation linker and target (`x86_64-unknown-linux-gnu`).