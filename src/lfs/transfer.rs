use super::{Cache, Pointer, cache, pointer::LIMIT};
use anyhow::{Context, Result, ensure};
use gix::bstr::ByteSlice;
use reqwest::{
    Url,
    blocking::{Client, RequestBuilder, Response},
    header::{HeaderMap, HeaderName, HeaderValue},
};
use serde::Deserialize;
use std::{
    collections::{BTreeMap, HashSet},
    fs,
    io::Read,
    time::Duration,
};

const MEDIA: &str = "application/vnd.git-lfs+json";

#[derive(Deserialize)]
struct Batch {
    transfer: Option<String>,
    hash_algo: Option<String>,
    objects: Vec<Object>,
}
#[derive(Deserialize)]
struct Object {
    oid: String,
    size: u64,
    actions: Option<BTreeMap<String, Action>>,
    error: Option<RemoteError>,
}
#[derive(Deserialize)]
struct RemoteError {
    code: u16,
}
#[derive(Deserialize)]
struct Action {
    href: String,
    #[serde(default)]
    header: BTreeMap<String, String>,
}

pub(super) struct Session<'repo> {
    repo: &'repo gix::Repository,
    client: Client,
    endpoint: Url,
}

fn checked_url(value: &str) -> Result<Url> {
    let url = Url::parse(value).context("invalid LFS HTTP endpoint")?;
    ensure!(
        matches!(url.scheme(), "http" | "https") && url.host_str().is_some(),
        "LFS requires an HTTP(S) endpoint"
    );
    ensure!(
        url.password().is_none() && url.username().is_empty(),
        "LFS URL credentials are refused; use the configured credential helper"
    );
    ensure!(
        url.fragment().is_none(),
        "LFS endpoint must not have a URL fragment"
    );
    Ok(url)
}

fn endpoint(repo: &gix::Repository, remote: &str, upload: bool) -> Result<Url> {
    let config = repo.config_snapshot();
    let keys = if upload {
        vec![
            "lfs.pushurl".to_owned(),
            "lfs.url".into(),
            format!("remote.{remote}.lfspushurl"),
            format!("remote.{remote}.lfsurl"),
        ]
    } else {
        vec!["lfs.url".to_owned(), format!("remote.{remote}.lfsurl")]
    };
    for key in &keys {
        if let Some(value) = config.string(key.as_str()) {
            return checked_url(value.to_str()?);
        }
    }
    if let Some(workdir) = repo.workdir() {
        let file = workdir.join(".lfsconfig");
        if file.is_file() {
            let file = gix::config::File::from_bytes_owned(
                &mut fs::read(file)?,
                gix::config::file::Metadata::from(gix::config::Source::Local),
                Default::default(),
            )?;
            for key in &keys {
                if let Some(value) = file.string(key.as_str()) {
                    return checked_url(value.to_str()?);
                }
            }
        }
    }
    let remote_config = repo.find_remote(remote)?;
    let remote_url = remote_config
        .url(if upload {
            gix::remote::Direction::Push
        } else {
            gix::remote::Direction::Fetch
        })
        .context("remote has no URL")?;
    ensure!(
        matches!(
            remote_url.scheme,
            gix::url::Scheme::Http | gix::url::Scheme::Https
        ),
        "this LFS transport needs lfs.url; SSH authentication discovery is not yet supported"
    );
    let mut url = checked_url(remote_url.to_bstring().to_str()?)?;
    let path = url.path().trim_end_matches('/');
    let path = if path.ends_with(".git") {
        format!("{path}/info/lfs")
    } else {
        format!("{path}.git/info/lfs")
    };
    url.set_path(&path);
    Ok(url)
}

impl<'repo> Session<'repo> {
    pub(super) fn new(repo: &'repo gix::Repository, remote: &str, upload: bool) -> Result<Self> {
        let endpoint = endpoint(repo, remote, upload)?;
        let config = repo.config_snapshot();
        let mut client = Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .user_agent(concat!("git-next-lfs/", env!("CARGO_PKG_VERSION")))
            .connect_timeout(Duration::from_secs(30));
        if let Some(timeout) = config.integer("http.timeout") {
            ensure!(timeout >= 0, "invalid HTTP timeout");
            if timeout != 0 {
                client = client.timeout(Duration::from_secs(timeout as u64));
            }
        }
        let ca_file = std::env::var_os("GIT_SSL_CAINFO")
            .map(std::path::PathBuf::from)
            .or(config.trusted_path("http.sslCAInfo")?);
        if let Some(path) = ca_file {
            let certificates = reqwest::Certificate::from_pem_bundle(&fs::read(path)?)?;
            ensure!(
                !certificates.is_empty(),
                "LFS CA file contains no certificates"
            );
            client = client.tls_certs_merge(certificates);
        }
        let verify = std::env::var("GIT_SSL_NO_VERIFY")
            .ok()
            .filter(|v| {
                !v.is_empty()
                    && v != "0"
                    && !["false", "no", "off"]
                        .iter()
                        .any(|flag| v.eq_ignore_ascii_case(flag))
            })
            .is_none()
            && config.boolean("http.sslVerify").unwrap_or(true);
        if !verify {
            client = client.tls_danger_accept_invalid_certs(true);
        }
        if let Some(proxy) = config.string("http.proxy") {
            if proxy.is_empty() {
                client = client.no_proxy();
            } else {
                client = client.proxy(reqwest::Proxy::all(proxy.to_str()?)?);
            }
        }
        Ok(Self {
            repo,
            client: client.build()?,
            endpoint,
        })
    }

    fn request(
        &self,
        url: &Url,
        headers: &BTreeMap<String, String>,
        make: impl Fn() -> Result<RequestBuilder>,
    ) -> Result<Response> {
        ensure!(
            self.endpoint.scheme() != "https" || url.scheme() == "https",
            "LFS HTTPS downgrade refused"
        );
        let mut parsed = HeaderMap::new();
        for (key, value) in headers {
            parsed.insert(
                HeaderName::from_bytes(key.as_bytes())?,
                HeaderValue::from_str(value)?,
            );
        }
        ensure!(
            !parsed.contains_key(reqwest::header::HOST)
                && !parsed.contains_key(reqwest::header::CONTENT_LENGTH),
            "LFS action cannot override transport headers"
        );
        let response = make()?
            .headers(parsed.clone())
            .send()
            .map_err(reqwest::Error::without_url)?;
        if response.status() != reqwest::StatusCode::UNAUTHORIZED {
            return Ok(response);
        }
        ensure!(
            !parsed.contains_key(reqwest::header::AUTHORIZATION),
            "LFS action authorization was rejected"
        );
        ensure!(
            url.scheme() == "https",
            "LFS credentials over plaintext HTTP are refused"
        );
        let mut read = crate::credentials::read_existing(self.repo);
        let mut action = gix::credentials::helper::Action::get_for_url(url.as_str());
        if let Some(context) = action.context_mut() {
            context.www_authenticate = response
                .headers()
                .get_all(reqwest::header::WWW_AUTHENTICATE)
                .iter()
                .map(|value| value.as_bytes().as_bstr().to_owned())
                .collect();
        }
        let outcome = read(action)?.context("no existing LFS credentials were provided")?;
        make()?
            .headers(parsed)
            .basic_auth(outcome.identity.username, Some(outcome.identity.password))
            .send()
            .map_err(|error| error.without_url().into())
    }

    fn batch(
        &self,
        pointers: &[Pointer],
        upload: bool,
        reference: Option<&str>,
    ) -> Result<Vec<Object>> {
        let mut url = self.endpoint.clone();
        url.set_path(&format!(
            "{}/objects/batch",
            url.path().trim_end_matches('/')
        ));
        let mut body = serde_json::json!({"operation":if upload {"upload"} else {"download"},"transfers":["basic"],"hash_algo":"sha256","objects":pointers});
        if let Some(reference) = reference {
            body["ref"] = serde_json::json!({"name":reference});
        }
        let mut response = self.request(&url, &BTreeMap::new(), || {
            Ok(self
                .client
                .post(url.clone())
                .header("Accept", MEDIA)
                .header("Content-Type", MEDIA)
                .json(&body))
        })?;
        ensure!(
            response.status() == reqwest::StatusCode::OK,
            "LFS batch failed with HTTP {}",
            response.status().as_u16()
        );
        ensure!(
            response
                .content_length()
                .is_none_or(|len| len <= 8 * 1024 * 1024),
            "oversized LFS batch response"
        );
        let mut bytes = Vec::new();
        response
            .by_ref()
            .take(8 * 1024 * 1024 + 1)
            .read_to_end(&mut bytes)
            .map_err(|_| anyhow::anyhow!("could not read the LFS batch response body"))?;
        ensure!(
            bytes.len() <= 8 * 1024 * 1024,
            "oversized LFS batch response"
        );
        let batch: Batch = serde_json::from_slice(&bytes).context("invalid LFS batch response")?;
        ensure!(
            batch.transfer.as_deref().is_none_or(|v| v == "basic")
                && batch.hash_algo.as_deref().is_none_or(|v| v == "sha256"),
            "unsupported LFS transfer or hash algorithm"
        );
        let expected: BTreeMap<_, _> = pointers
            .iter()
            .map(|pointer| (pointer.oid.as_str(), pointer.size))
            .collect();
        let mut seen = HashSet::new();
        for object in &batch.objects {
            ensure!(
                expected.get(object.oid.as_str()) == Some(&object.size)
                    && seen.insert(object.oid.as_str()),
                "LFS batch returned an unexpected, mismatched or duplicate object"
            );
            if let Some(error) = &object.error {
                anyhow::bail!(
                    "LFS object {} was rejected with status {}",
                    object.oid,
                    error.code
                );
            }
        }
        ensure!(
            seen.len() == expected.len(),
            "LFS batch omitted requested objects"
        );
        Ok(batch.objects)
    }

    pub(super) fn download(
        &self,
        cache: &Cache,
        pointers: &[Pointer],
        reference: Option<&str>,
    ) -> Result<()> {
        let mut needed = Vec::new();
        for pointer in pointers {
            if pointer.size != 0 && cache.verified(pointer)?.is_none() {
                needed.push(pointer.clone());
            }
        }
        for batch in needed.chunks(100) {
            for object in self.batch(batch, false, reference)? {
                let action = object
                    .actions
                    .as_ref()
                    .and_then(|actions| actions.get("download"))
                    .context("LFS response has no download action")?;
                let url = checked_url(&action.href)?;
                let mut response =
                    self.request(&url, &action.header, || Ok(self.client.get(url.clone())))?;
                ensure!(
                    response.status() == reqwest::StatusCode::OK,
                    "LFS download failed with HTTP {}",
                    response.status().as_u16()
                );
                ensure!(
                    response
                        .content_length()
                        .is_none_or(|size| size == object.size),
                    "LFS download content length mismatch"
                );
                let mut temporary = cache.temporary()?;
                let actual = cache::hash(
                    response
                        .by_ref()
                        .take(object.size.checked_add(1).context("invalid LFS size")?),
                    temporary.as_file_mut(),
                )
                .map_err(|_| {
                    anyhow::anyhow!("could not read or store the complete LFS download")
                })?;
                ensure!(
                    actual.size == object.size && actual.oid == object.oid,
                    "LFS downloaded object checksum or size mismatch"
                );
                cache.publish(temporary, &actual)?;
            }
        }
        Ok(())
    }

    pub(super) fn upload(
        &self,
        cache: &Cache,
        pointers: &[Pointer],
        reference: Option<&str>,
    ) -> Result<()> {
        for batch in pointers.chunks(100) {
            // Validate every local object before asking the server to change anything.
            for pointer in batch {
                cache
                    .verified(pointer)?
                    .context("local LFS object needed for upload is missing")?;
            }
            for object in self.batch(batch, true, reference)? {
                let Some(action) = object
                    .actions
                    .as_ref()
                    .and_then(|actions| actions.get("upload"))
                else {
                    continue;
                };
                let pointer = Pointer::new(object.oid, object.size);
                let url = checked_url(&action.href)?;
                let response = self.request(&url, &action.header, || {
                    let file = cache
                        .verified(&pointer)?
                        .context("LFS cache object disappeared before upload")?;
                    Ok(self
                        .client
                        .put(url.clone())
                        .header("Content-Type", "application/octet-stream")
                        .header("Content-Length", pointer.size)
                        .body(file))
                })?;
                ensure!(
                    response.status().is_success(),
                    "LFS upload failed with HTTP {}",
                    response.status().as_u16()
                );
                if let Some(verify) = object
                    .actions
                    .as_ref()
                    .and_then(|actions| actions.get("verify"))
                {
                    let url = checked_url(&verify.href)?;
                    let response = self.request(&url, &verify.header, || {
                        Ok(self
                            .client
                            .post(url.clone())
                            .header("Accept", MEDIA)
                            .header("Content-Type", MEDIA)
                            .json(&pointer))
                    })?;
                    ensure!(
                        response.status() == reqwest::StatusCode::OK,
                        "LFS upload verification failed with HTTP {}",
                        response.status().as_u16()
                    );
                }
            }
        }
        Ok(())
    }
}

pub(super) fn pointers(
    repo: &gix::Repository,
    tips: impl IntoIterator<Item = gix::ObjectId>,
    stop: &HashSet<gix::ObjectId>,
) -> Result<Vec<Pointer>> {
    let objects = crate::push::reachable(repo, tips, stop, false)?;
    let mut pointers = BTreeMap::new();
    for id in objects {
        let header = repo.find_header(id)?;
        if header.kind() != gix::objs::Kind::Blob || header.size() >= LIMIT as u64 {
            continue;
        }
        let object = repo.find_object(id)?;
        if let Ok(pointer) = Pointer::parse(&object.data) {
            if pointer.size == 0 {
                continue;
            }
            ensure!(
                pointer.extensions.is_empty(),
                "LFS pointer extension transformations are not supported"
            );
            if let Some(old) = pointers.insert(pointer.oid.clone(), pointer.clone()) {
                ensure!(
                    old.size == pointer.size,
                    "same LFS object identifier has conflicting sizes"
                );
            }
        }
    }
    Ok(pointers.into_values().collect())
}

pub(crate) fn upload_before_push(
    repo: &gix::Repository,
    remote: &str,
    tips: Vec<gix::ObjectId>,
    known: Vec<gix::ObjectId>,
    reference: Option<&str>,
) -> Result<()> {
    if std::env::var("GIT_LFS_SKIP_PUSH")
        .is_ok_and(|v| !v.is_empty() && v != "0" && !v.eq_ignore_ascii_case("false"))
    {
        return Ok(());
    }
    let known = crate::push::reachable(repo, known, &HashSet::new(), true)?;
    let pointers = pointers(repo, tips, &known)?;
    if pointers.is_empty() {
        return Ok(());
    }
    Session::new(repo, remote, true)?.upload(&Cache::new(repo)?, &pointers, reference)
}
