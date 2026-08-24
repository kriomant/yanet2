use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::{
    CompleteEnv,
    engine::{ArgValueCandidates, CompletionCandidate},
};
use portfwdpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    portfwd_service_client::PortfwdServiceClient,
};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod portfwdpb {
    use serde::Serialize;

    tonic::include_proto!("modules.portfwd.controlplane.portfwdpb.v1");
}

/// Portfwd module, diverting traffic by TCP/UDP source port.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Show(ShowConfigCmd),
    Update(UpdateConfigCmd),
    Delete(DeleteConfigCmd),
}

/// Pipeline of the target device that diverted packets re-enter.
#[derive(Debug, Clone, Copy, ValueEnum)]
pub enum ForwardModeArg {
    In,
    Out,
}

impl From<ForwardModeArg> for portfwdpb::ForwardMode {
    fn from(mode: ForwardModeArg) -> Self {
        match mode {
            ForwardModeArg::In => Self::In,
            ForwardModeArg::Out => Self::Out,
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Portfwd module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// Portfwd module name to create or replace.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// TCP/UDP source port that takes the alternative exit.
    #[arg(long, short, required = true)]
    pub port: Vec<u16>,
    /// Device that matching packets are diverted to.
    #[arg(long, short)]
    pub target: String,
    /// Pipeline of the target device to divert into.
    #[arg(long, short, default_value = "out")]
    pub mode: ForwardModeArg,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// Portfwd module name to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.portfwd.controlplane.portfwdpb.v1.PortfwdService";

fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    start();
}

#[tokio::main(flavor = "current_thread")]
async fn start() {
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = PortfwdService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
    }
}

pub struct PortfwdService {
    client: PortfwdServiceClient<LayeredChannel>,
    endpoint: String,
}

impl PortfwdService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|e| Error::from_connection(e, "connect", &connection.endpoint))?;
        let client = PortfwdServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self {
            client,
            endpoint: connection.endpoint.clone(),
        })
    }

    fn map_err<'a>(&'a self, action: &'a str) -> impl FnOnce(tonic::Status) -> Error + 'a {
        let endpoint = self.endpoint.clone();
        move |status| Error::from_status(status, action, endpoint, SERVICE_NAME)
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self
            .client
            .list_configs(request)
            .await
            .map_err(self.map_err("list"))?
            .into_inner();
        log::debug!("list configs response: {response:?}");

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No portfwd configurations found."),
                        format_args!(
                            "create one with 'yanet-cli-portfwd update --name <name> --target <device> --port <port>'"
                        ),
                    );
                    return;
                }

                for name in &response.configs {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        log::trace!("show config request: {request:?}");
        let response = self
            .client
            .show_config(request)
            .await
            .map_err(self.map_err("show"))?
            .into_inner();
        log::debug!("show config response: {response:?}");

        output::data(
            || &response,
            || {
                let Some(config) = response.config.as_ref() else {
                    return;
                };

                println!("name: {}", config.name);
                println!("target: {}", config.target);
                println!("mode: {}", mode_name(config.mode));
                println!(
                    "ports: {}",
                    config.ports.iter().map(u32::to_string).collect::<Vec<_>>().join(", ")
                );
            },
        );

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateConfigCmd) -> Result<(), Error> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            ports: cmd.port.iter().map(|port| u32::from(*port)).collect(),
            target: cmd.target.clone(),
            mode: portfwdpb::ForwardMode::from(cmd.mode).into(),
        };
        log::trace!("update config request: {request:?}");
        let response = self
            .client
            .update_config(request)
            .await
            .map_err(self.map_err("update"))?
            .into_inner();
        log::debug!("update config response: {response:?}");

        output::success("update", format_args!("Updated {}.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteConfigCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        log::trace!("delete config request: {request:?}");
        let response = self
            .client
            .delete_config(request)
            .await
            .map_err(self.map_err("delete"))?
            .into_inner();
        log::debug!("delete config response: {response:?}");

        output::success("delete", format_args!("Deleted {}.", cmd.config_name));

        Ok(())
    }
}

/// Renders a wire mode value, falling back to the raw number for a value this
/// build does not know.
fn mode_name(mode: i32) -> String {
    match portfwdpb::ForwardMode::try_from(mode) {
        Ok(portfwdpb::ForwardMode::None) => "NONE".to_owned(),
        Ok(portfwdpb::ForwardMode::In) => "IN".to_owned(),
        Ok(portfwdpb::ForwardMode::Out) => "OUT".to_owned(),
        Err(_) => mode.to_string(),
    }
}

/// Completion candidates for a `--name` argument: the portfwd configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            PortfwdServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}
