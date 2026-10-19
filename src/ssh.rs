use anyhow::{Context, Result, bail, ensure};
use gix::bstr::{BStr, ByteSlice};
use gix_transport::{
    Protocol, Service,
    client::{
        self, TransportWithoutIO,
        blocking_io::{RequestWriter, SetServiceResponse, Transport},
    },
};
use russh::keys::PublicKeyOrCertificate;
use russh::{client as ssh_client, keys};
use std::{
    borrow::Cow,
    fs,
    io::{BufReader, Read, Write},
    path::{Path, PathBuf},
    sync::{Arc, OnceLock},
    time::Duration,
};
use tokio_util::io::SyncIoBridge;

static OPTIONS: OnceLock<(Option<PathBuf>, Option<PathBuf>)> = OnceLock::new();
static RUNTIME: OnceLock<tokio::runtime::Runtime> = OnceLock::new();

pub fn configure(identity: Option<PathBuf>, known: Option<PathBuf>) -> Result<()> {
    OPTIONS
        .set((identity, known))
        .map_err(|_| anyhow::anyhow!("SSH options already initialized"))
}

fn runtime() -> Result<&'static tokio::runtime::Runtime> {
    if let Some(runtime) = RUNTIME.get() {
        return Ok(runtime);
    }
    let rt = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .enable_all()
        .build()?;
    let _ = RUNTIME.set(rt);
    RUNTIME.get().context("SSH runtime unavailable")
}

fn home() -> Result<PathBuf> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
        .context("HOME/USERPROFILE is unset")
}

#[derive(Debug)]
struct Host {
    name: String,
    user: String,
    port: u16,
    identity: Vec<PathBuf>,
    known: PathBuf,
    alias: Option<String>,
    identities_only: bool,
}

fn expand(value: &str, host: &str, user: &str, port: u16) -> Result<PathBuf> {
    let value = value
        .trim_matches('"')
        .replace("%h", host)
        .replace("%r", user)
        .replace("%p", &port.to_string());
    if let Some(rest) = value.strip_prefix("~/") {
        Ok(home()?.join(rest))
    } else {
        Ok(PathBuf::from(value))
    }
}

fn parse_config(path: &Path, host: &str, values: &mut BTreeValues, depth: usize) -> Result<()> {
    ensure!(depth < 16, "SSH Include nesting limit exceeded");
    let text = match fs::read_to_string(path) {
        Ok(text) => text,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(e.into()),
    };
    let mut active = true;
    for raw in text.lines() {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let (key, value) = line
            .split_once([' ', '\t', '='])
            .context("invalid SSH configuration line")?;
        let key = key.to_ascii_lowercase();
        let value = value.trim_start_matches([' ', '\t', '=']).trim();
        if key == "host" {
            let mut positive = false;
            let mut excluded = false;
            for p in value.split_whitespace() {
                let neg = p.starts_with('!');
                let p = p.trim_start_matches('!').to_ascii_lowercase();
                let matched = glob::Pattern::new(&p)?.matches(&host.to_ascii_lowercase());
                if matched {
                    if neg {
                        excluded = true
                    } else {
                        positive = true
                    }
                }
            }
            active = positive && !excluded;
            continue;
        }
        if key == "match" {
            bail!(
                "SSH Match rules are not supported yet; refusing to ignore security-sensitive configuration"
            )
        }
        if !active {
            continue;
        }
        if key == "include" {
            for pattern in value.split_whitespace() {
                let pattern = expand(pattern, host, "", 22)?;
                let pattern = if pattern.is_absolute() {
                    pattern
                } else {
                    path.parent()
                        .context("SSH config directory missing")?
                        .join(pattern)
                };
                for included in glob::glob(&pattern.to_string_lossy())? {
                    parse_config(&included?, host, values, depth + 1)?
                }
            }
        } else if matches!(
            key.as_str(),
            "proxycommand"
                | "proxyjump"
                | "localcommand"
                | "identityagent"
                | "certificatefile"
                | "pkcs11provider"
                | "securitykeyprovider"
        ) && value != "none"
        {
            bail!(
                "SSH option {key} is not implemented; no external command fallback will be executed"
            )
        } else if key == "identityfile" {
            values.identities.push(value.to_owned())
        } else {
            values.single.entry(key).or_insert_with(|| value.to_owned());
        }
    }
    Ok(())
}

#[derive(Default)]
struct BTreeValues {
    single: std::collections::BTreeMap<String, String>,
    identities: Vec<String>,
}

fn host(url: &gix::Url) -> Result<Host> {
    let alias = url.host().context("SSH URL has no hostname")?;
    ensure!(
        !alias.starts_with('-') && !alias.contains(['\n', '\r', '\0']),
        "invalid SSH hostname"
    );
    let mut cfg = BTreeValues::default();
    parse_config(&home()?.join(".ssh/config"), alias, &mut cfg, 0)?;
    let name = cfg
        .single
        .get("hostname")
        .cloned()
        .unwrap_or_else(|| alias.to_owned());
    let user = url
        .user()
        .map(str::to_owned)
        .or_else(|| cfg.single.get("user").cloned())
        .or_else(|| std::env::var("USER").ok())
        .or_else(|| std::env::var("USERNAME").ok())
        .context("SSH user is not configured")?;
    let port = url
        .port
        .or(cfg
            .single
            .get("port")
            .map(|v| v.parse::<u16>())
            .transpose()
            .context("invalid SSH port")?)
        .unwrap_or(22);
    ensure!(port != 0, "SSH port must be positive");
    let options = OPTIONS.get();
    let identity = if let Some(path) = options.and_then(|o| o.0.as_ref()) {
        vec![path.clone()]
    } else {
        cfg.identities
            .iter()
            .map(|v| expand(v, &name, &user, port))
            .collect::<Result<Vec<_>>>()?
    };
    let known = options
        .and_then(|o| o.1.clone())
        .or_else(|| cfg.single.get("userknownhostsfile").map(PathBuf::from))
        .unwrap_or(home()?.join(".ssh/known_hosts"));
    let known = expand(&known.to_string_lossy(), &name, &user, port)?;
    Ok(Host {
        name,
        user,
        port,
        identity,
        known,
        alias: cfg.single.get("hostkeyalias").cloned(),
        identities_only: cfg
            .single
            .get("identitiesonly")
            .is_some_and(|x| x.eq_ignore_ascii_case("yes")),
    })
}

struct Verify {
    host: String,
    port: u16,
    known: PathBuf,
}
impl ssh_client::Handler for Verify {
    type Error = anyhow::Error;
    async fn check_server_key(&mut self, key: &PublicKeyOrCertificate) -> Result<bool> {
        ensure!(
            key.certificate().is_none(),
            "SSH host certificates are not supported yet"
        );
        let content = fs::read_to_string(&self.known).context("known_hosts cannot be read")?;
        ensure!(
            !content
                .lines()
                .any(|line| line.trim_start().starts_with('@')),
            "marked known_hosts entries require unsupported certificate/revocation semantics; refusing connection"
        );
        let key = key.public_key();
        let known = keys::check_known_hosts_path(&self.host, self.port, &key, &self.known)
            .context("SSH host key changed or known_hosts is invalid")?;
        ensure!(
            known,
            "SSH host key is not trusted: {}:{} {}. Verify the fingerprint independently and provision known_hosts; automatic acceptance is disabled",
            self.host,
            self.port,
            key.fingerprint(keys::HashAlg::Sha256)
        );
        Ok(true)
    }
}

struct Native<R: Read, W: Write> {
    inner: Option<gix_transport::client::git::blocking_io::Connection<R, W>>,
    _session: ssh_client::Handle<Verify>,
    url: gix::bstr::BString,
}
impl<R: Read, W: Write> Drop for Native<R, W> {
    fn drop(&mut self) {
        if let Some(runtime) = RUNTIME.get() {
            // Russh closes channel streams asynchronously when their final owner drops.
            let _entered = runtime.enter();
            drop(self.inner.take());
        }
    }
}
impl<R: Read, W: Write> TransportWithoutIO for Native<R, W> {
    fn to_url(&self) -> Cow<'_, BStr> {
        Cow::Borrowed(self.url.as_bstr())
    }
    fn supported_protocol_versions(&self) -> &[Protocol] {
        &[Protocol::V1, Protocol::V2]
    }
    fn connection_persists_across_multiple_requests(&self) -> bool {
        true
    }
    fn configure(&mut self, _config: &dyn std::any::Any) -> gix::Result {
        Ok(())
    }
}
impl<R: Read, W: Write> Transport for Native<R, W> {
    fn handshake<'a>(
        &mut self,
        service: Service,
        parameters: &'a [(&'a str, Option<&'a str>)],
    ) -> std::result::Result<SetServiceResponse<'_>, client::Error> {
        self.inner
            .as_mut()
            .expect("transport active until drop")
            .handshake(service, parameters)
    }
    fn request(
        &mut self,
        mode: client::WriteMode,
        end: client::MessageKind,
        trace: bool,
    ) -> std::result::Result<RequestWriter<'_>, client::Error> {
        self.inner
            .as_mut()
            .expect("transport active until drop")
            .request(mode, end, trace)
    }
}

fn quote_path(path: &str) -> Result<String> {
    ensure!(
        !path.is_empty() && !path.starts_with('-') && !path.contains(['\0', '\r', '\n']),
        "invalid remote repository path"
    );
    Ok(format!("'{}'", path.replace('\'', "'\\''")))
}

pub fn transport(url: &gix::Url, version: Protocol) -> Result<Box<dyn Transport + Send>> {
    let host = host(url)?;
    let rt = runtime()?;
    rt.block_on(async {
        let verifier=Verify{host:host.alias.clone().unwrap_or(host.name.clone()),port:host.port,known:host.known};
        let cfg=ssh_client::Config{inactivity_timeout:Some(Duration::from_secs(120)),window_size:4*1024*1024,maximum_packet_size:64*1024,..Default::default()};
        let mut session=tokio::time::timeout(Duration::from_secs(20),ssh_client::connect(Arc::new(cfg),(host.name.as_str(),host.port),verifier)).await.context("SSH connection timed out")??;
        let mut authenticated=false;
        if !host.identities_only && host.identity.is_empty() {
            #[cfg(unix)]
            let agent=keys::agent::client::AgentClient::connect_env().await;
            #[cfg(windows)]
            let agent=keys::agent::client::AgentClient::connect_named_pipe(r"\\.\pipe\openssh-ssh-agent").await;
            if let Ok(mut agent)=agent {
                for identity in agent.request_identities().await?.into_iter().take(3) {
                    if session.authenticate_publickey_with(&host.user,identity.public_key().into_owned(),None,&mut agent).await?.success(){authenticated=true;break}
                }
            }
        }
        if !authenticated {
            let identities=if host.identity.is_empty(){vec![home()?.join(".ssh/id_ed25519"),home()?.join(".ssh/id_rsa")]}else{host.identity};
            for path in identities.into_iter().take(3).filter(|p|p.is_file()) {
                let passphrase=std::env::var("GIT_NEXT_SSH_KEY_PASSPHRASE").ok();
                let key=keys::load_secret_key(&path,passphrase.as_deref()).context("could not load SSH key (encrypted keys accept GIT_NEXT_SSH_KEY_PASSPHRASE)")?;
                let hash=session.best_supported_rsa_hash().await?.flatten();
                if session.authenticate_publickey(&host.user,keys::PrivateKeyWithHashAlg::new(Arc::new(key),hash)).await?.success(){authenticated=true;break}
            }
        }
        if !authenticated && let Ok(password)=std::env::var("GIT_NEXT_SSH_PASSWORD") {authenticated=session.authenticate_password(&host.user,password).await?.success()}
        ensure!(authenticated,"SSH authentication failed; no external ssh process or repeated password attempts were used");
        let channel=session.channel_open_session().await?;
        if version==Protocol::V2 {channel.set_env(false,"GIT_PROTOCOL","version=2").await?;}
        channel.exec(true,format!("git-upload-pack {}",quote_path(url.path.to_str()?)?)).await?;
        let (reader,writer)=tokio::io::split(channel.into_stream());
        let read=BufReader::with_capacity(64*1024,SyncIoBridge::new_with_handle(reader,rt.handle().clone()));
        let write=SyncIoBridge::new_with_handle(writer,rt.handle().clone());
        let connection=gix_transport::client::git::blocking_io::Connection::new(read,write,version,url.path.clone(),None::<(String,Option<u16>)>,gix_transport::client::git::ConnectMode::Process,false);
        Ok(Box::new(Native{inner:Some(connection),_session:session,url:url.to_bstring()}) as Box<dyn Transport+Send>)
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn quote_remote_path() {
        assert_eq!(quote_path("a'b.git").expect("valid"), "'a'\\''b.git'");
        assert!(quote_path("-o injected").is_err());
        assert!(quote_path("a\nb").is_err());
    }
    #[test]
    fn reject_proxy() {
        let dir = tempfile::tempdir().expect("temp");
        let file = dir.path().join("config");
        fs::write(&file, "Host example\n ProxyCommand dangerous\n").expect("write");
        assert!(parse_config(&file, "example", &mut BTreeValues::default(), 0).is_err());
    }
}
