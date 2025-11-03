use axum::{
    extract::{Path, Query, State},
    http::StatusCode,
    response::Redirect,
    routing::{get, post},
    Json, Router,
};
use rand::distr::{Alphanumeric, SampleString};
use redb::{Database, ReadableDatabase, ReadableTable, TableDefinition};
use serde::{Deserialize, Serialize};
use std::net::SocketAddr;
use std::sync::Arc;

type SharedDb = Arc<Database>;

const TABLE: TableDefinition<&str, &str> = TableDefinition::new("urls");

#[derive(Deserialize)]
struct CreateShortLink {
    url: String,
    alias: Option<String>,
}

#[derive(Serialize)]
struct ShortLinkResponse {
    short_link: String,
}

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

async fn redirect_link(
    State(db): State<SharedDb>,
    Path(alias): Path<String>,
) -> Result<Redirect, StatusCode> {
    let read_txn = db.begin_read().map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;
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
    let db = Arc::new(Database::create("link_shortener.redb").unwrap());
    {
        let write_txn = db.begin_write().unwrap();
        write_txn.open_table(TABLE).unwrap();
        write_txn.commit().unwrap();
    }

    let app = Router::new()
        .route("/", post(create_short_link))
        .route("/{alias}", get(redirect_link))
        .with_state(db);

    let addr = SocketAddr::from(([0, 0, 0, 0], 80));
    println!("Listening on {}", addr);
    let listener = tokio::net::TcpListener::bind(addr).await.unwrap();
    axum::serve(listener, app).await.unwrap();
}
