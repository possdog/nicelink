// Minimal link-shortener in Rust: Axum + redb.
// Listens on 80 (HTTP) or 443 (HTTPS) depending on certificate presence.

use axum::{
    Json, Router,
    extract::{Path, Query, State},
    http::StatusCode,
    response::Redirect,
    routing::{get, post},
};
use rand::distr::{Alphanumeric, SampleString};
use redb::{Database, ReadableDatabase, ReadableTable, TableDefinition};
use serde::{Deserialize, Serialize};
use std::net::SocketAddr;
use std::path::Path as FilePath;
use std::sync::Arc;

// Shared handle to the embedded database.
type SharedDb = Arc<Database>;

// Single table: key = short alias, value = original URL.
const TABLE: TableDefinition<&str, &str> = TableDefinition::new("urls");

// Expected query parameters for creating a short link.
#[derive(Deserialize)]
struct CreateShortLink {
    url: String,
    alias: Option<String>,
}

// Response returned when a short link is created.
#[derive(Serialize)]
struct ShortLinkResponse {
    short_link: String,
}

// POST / handler: create short link.
// Returns 201 on success, 400 for bad requests, 500 for server errors.
async fn create_short_link(
    State(db): State<SharedDb>,
    Query(params): Query<CreateShortLink>,
) -> Result<Json<ShortLinkResponse>, (StatusCode, String)> {
    if params.url.is_empty() {
        return Err((StatusCode::BAD_REQUEST, "Missing 'url' parameter".into()));
    }

    let alias = if let Some(a) = params.alias {
        if a.is_empty() {
            return Err((StatusCode::BAD_REQUEST, "Alias cannot be empty".into()));
        }
        a
    } else {
        Alphanumeric.sample_string(&mut rand::rng(), 5)
    };

    let write_txn = db
        .begin_write()
        .map_err(|e| (StatusCode::INTERNAL_SERVER_ERROR, e.to_string()))?;
    {
        let mut table = write_txn
            .open_table(TABLE)
            .map_err(|e| (StatusCode::INTERNAL_SERVER_ERROR, e.to_string()))?;
        if table.get(alias.as_str()).unwrap().is_some() {
            return Err((StatusCode::BAD_REQUEST, "Alias already exists".into()));
        }
        table
            .insert(alias.as_str(), params.url.as_str())
            .map_err(|e| (StatusCode::INTERNAL_SERVER_ERROR, e.to_string()))?;
    }
    write_txn
        .commit()
        .map_err(|e| (StatusCode::INTERNAL_SERVER_ERROR, e.to_string()))?;

    Ok(Json(ShortLinkResponse {
        short_link: format!("/{}", alias),
    }))
}

// GET /{alias} handler: redirect to original URL.
// Returns 302 on success, 404 if alias not found, 500 on server errors.
async fn redirect_link(
    State(db): State<SharedDb>,
    Path(alias): Path<String>,
) -> Result<Redirect, StatusCode> {
    let read_txn = db
        .begin_read()
        .map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;
    let table = read_txn
        .open_table(TABLE)
        .map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;
    let url = table
        .get(alias.as_str())
        .map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?
        .ok_or(StatusCode::NOT_FOUND)?
        .value()
        .to_string();

    Ok(Redirect::permanent(&url))
}

#[tokio::main]
async fn main() {
    // Open (or create) the on-disk database.
    let db = Arc::new(Database::create("link_shortener.redb").unwrap());
    {
        let write_txn = db.begin_write().unwrap();
        write_txn.open_table(TABLE).unwrap();
        write_txn.commit().unwrap();
    }

    // Build router with shared DB state.
    let app = Router::new()
        .route("/", post(create_short_link))
        .route("/{alias}", get(redirect_link))
        .with_state(db.clone());

    // HTTP task via regular axum
    let http = tokio::spawn({
        let app = app.clone();
        async move {
            let addr = SocketAddr::from(([0, 0, 0, 0], 80));
            println!("HTTP on {}", addr);
            let listener = tokio::net::TcpListener::bind(addr).await.unwrap();
            axum::serve(listener, app).await.unwrap();
        }
    });

    // HTTPS task via axum-server
    let https = tokio::spawn({
        async move {
            let cert = FilePath::new("domain.cert.pem");
            let key  = FilePath::new("private.key.pem");
            if !cert.exists() || !key.exists() {
                // Quietly skip HTTPS if certs missing
                return;
            }
            let rustls_config = axum_server::tls_rustls::RustlsConfig::from_pem_file(cert, key)
                .await
                .expect("TLS files");
            let addr = SocketAddr::from(([0, 0, 0, 0], 443));
            println!("HTTPS on {}", addr);
            axum_server::bind_rustls(addr, rustls_config)
                .serve(app.into_make_service())
                .await
                .unwrap();
        }
    });

    let (_, _) = tokio::join!(
        biased; // Prioritize polling the HTTPS server
        https, http
    );
}

