# tfdriftctl — Terraform Drift Detection

`tfdriftctl` compares resources in Terraform state with live cloud resources and reports missing resources, unmanaged resources, changed attributes, and changed tags. It is based on [terraform-drift-detector](https://github.com/iam-veeramalla/terraform-drift-detector) and adds Slack delivery and independent JSON report export.

Terraform is the first-class and only workflow. The tool reads local, HTTP(S), or S3 Terraform state directly; it does not run `terraform plan` or `terraform apply`.

## Features

- Terraform state from a local file, HTTP(S), or S3
- Live AWS discovery for EC2 instances, VPCs, subnets, security groups, and S3 buckets
- Live Azure subscription discovery through Azure Resource Graph
- Secretless Azure DevOps authentication with workload-identity service connections
- Table and JSON CLI output
- Optional JSON artifact alongside human-readable table output
- Slack notifications through a bot token or incoming webhook
- Multiple Slack channels when using a bot token
- On-demand scans through the CLI or REST API
- Scheduled scans and a web dashboard
- SQLite persistence for workspaces, scans, and schedules

## Build and test

Requires Go 1.24 or later and CGO support for SQLite.

```bash
make test
make build
```

The binaries are written to `bin/tfdriftctl` and `bin/tfdrift-server`.

## Run scans

```bash
# State-only smoke test (all state resources appear missing because cloud lookup is skipped)
./bin/tfdriftctl scan \
  --state testdata/state/sample.tfstate \
  --provider aws \
  --skip-cloud

# Live AWS scan with table output and a JSON artifact
./bin/tfdriftctl scan \
  --state /path/to/terraform.tfstate \
  --provider aws \
  --region us-east-1 \
  --output table \
  --json-file drift-report.json

# JSON on standard output
./bin/tfdriftctl scan \
  --state /path/to/terraform.tfstate \
  --provider aws \
  --output json

# Scan a configured workspace
./bin/tfdriftctl scan --config configs/driftctl.yaml --workspace prod
```

Exit codes are `0` for no drift, `1` when drift is detected, and `2` for a command or scan error.

## Azure live discovery

Azure scans query Azure Resource Graph across one or more subscriptions. ARM resource IDs are matched case-insensitively against IDs from `azurerm` Terraform state. The provider detects missing and unmanaged resources, tag changes, and selected live attributes for resource groups, storage accounts, virtual networks, subnets, virtual machines, and public IPs.

```bash
# Local development after `az login`
./bin/tfdriftctl scan \
  --provider azure \
  --state terraform.tfstate \
  --subscription 00000000-0000-0000-0000-000000000000 \
  --output table \
  --json-file azure-drift.json
```

Subscription IDs can be repeated with `--subscription`, supplied as `subscriptions` on a configured workspace, inferred from ARM IDs in state, or read from `AZURE_SUBSCRIPTION_ID`.

For local and Azure-hosted execution, Azure authentication falls back to `DefaultAzureCredential`. This supports Azure CLI login, managed identity, and standard Azure environment credentials.

### Azure DevOps OIDC

Create an Azure Resource Manager service connection using workload identity federation and grant its identity at least `Reader` access to every subscription being scanned. Resource Graph only returns resources the identity is authorized to read.

Run `tfdriftctl` inside `AzureCLI@2`. That task supplies the tenant, client, service-connection, and OIDC request values. The pipeline must explicitly map `$(System.AccessToken)` to `SYSTEM_ACCESSTOKEN`; `tfdriftctl` then selects `AzurePipelinesCredential` automatically.

A complete build, scan, and JSON artifact example is provided in [examples/azure-pipelines.yml](examples/azure-pipelines.yml):

```yaml
- task: AzureCLI@2
  env:
    SYSTEM_ACCESSTOKEN: $(System.AccessToken)
  inputs:
    azureSubscription: my-workload-identity-service-connection
    scriptType: bash
    scriptLocation: inlineScript
    inlineScript: |
      read -r subscription_id < <(az account show --query id -o tsv)
      ./bin/tfdriftctl scan \
        --provider azure \
        --state terraform.tfstate \
        --subscription "$subscription_id" \
        --json-file "$(Build.ArtifactStagingDirectory)/tfdrift-report.json"
```

No client secret is required. Do not map or print the system access token anywhere except the task environment.

## Slack notifications

### Bot token (recommended for channels)

Create a Slack app with the `chat:write` scope, install it to the workspace, invite it to each destination channel, and expose its token only through an environment variable:

```bash
export SLACK_BOT_TOKEN='xoxb-...'

./bin/tfdriftctl scan \
  --state terraform.tfstate \
  --provider aws \
  --slack-channel '#platform-alerts' \
  --slack-channel '#terraform-drift'
```

By default, CLI channel flags use `SLACK_BOT_TOKEN`. Override the variable name with `--slack-token-env`. Use channel IDs for private channels and environments where names are ambiguous.

### Incoming webhook

```bash
export SLACK_WEBHOOK_URL='https://hooks.slack.com/services/...'

./bin/tfdriftctl scan \
  --state terraform.tfstate \
  --provider aws \
  --slack-webhook-env SLACK_WEBHOOK_URL
```

An incoming webhook normally posts to the channel selected when the webhook was created. Some legacy webhooks allow `--slack-channel` to override it. Use a bot token for dependable multi-channel delivery.

Control notification events with `--slack-notify-on drift,error`, `clean`, or `always`. The default is `drift,error`.

### Configuration

Slack configured in `driftctl.yaml` applies to CLI, API, and scheduled scans:

```yaml
database: driftctl.db

api:
  addr: ":8080"

notifications:
  slack:
    enabled: true
    bot_token_env: SLACK_BOT_TOKEN
    channels:
      - "#platform-alerts"
      - "#terraform-drift"
    notify_on: [drift, error]
    max_findings: 10

workspaces:
  - name: prod
    provider: aws
    state:
      backend: s3
      bucket: my-tf-state
      key: prod/terraform.tfstate
      region: us-east-1
    regions: [us-east-1]
    compare:
      ignore_tags: [tfdriftctl_scan]
    schedule:
      cron: "0 */6 * * *"

  - name: azure-prod
    provider: azure
    state:
      backend: local
      path: azure.tfstate
    subscriptions:
      - 00000000-0000-0000-0000-000000000000
```

Do not put Slack tokens or webhook URLs in the YAML file. `bot_token_env` and `webhook_env` name the environment variables from which secrets are read.

## Reports

```bash
# Table output
./bin/tfdriftctl report SCAN_ID --output table

# JSON output and a persisted JSON artifact
./bin/tfdriftctl report SCAN_ID --output json --json-file report.json
```

JSON files are written atomically with owner-only (`0600`) permissions.

## Server and API

```bash
make run-server
# Dashboard: http://localhost:8080
```

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `GET` | `/api/v1/workspaces` | List workspaces |
| `POST` | `/api/v1/workspaces` | Create a workspace |
| `POST` | `/api/v1/workspaces/{id}/scans` | Run a scan |
| `GET` | `/api/v1/scans` | List recent scans |
| `GET` | `/api/v1/scans/{id}/report?format=json` | Fetch JSON or table report |
| `PUT` | `/api/v1/workspaces/{id}/schedules` | Create or update a schedule |

Set `api.api_key` to require `X-API-Key` (or a bearer token) on API routes.

## Architecture

```text
Terraform state -> reader -> normalized expected resources --+
                                                           +-> drift engine -> report -> table / JSON / Slack
Cloud APIs     -> provider -> normalized actual resources  --+
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
