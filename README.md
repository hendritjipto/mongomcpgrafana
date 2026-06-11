# docker — MongoDB + Grafana Cloud observability stack

Builds and runs three sample applications that each connect to MongoDB and export
OpenTelemetry (traces/metrics/logs) to Grafana Alloy, which forwards to Grafana Cloud.

## Services

| Service              | What it does                                                      |
|----------------------|-------------------------------------------------------------------|
| `mongodb`            | MongoDB 7.0, the shared database the apps connect to              |
| `alloy`              | Receives OTLP from the apps and forwards to Grafana Cloud         |
| `grafana-pdc-agent`  | Private Data Source Connect tunnel so Grafana Cloud reaches mongo |
| `mongodb-mcp-server` | MongoDB MCP server over HTTP (`http://localhost:3000/mcp`)        |
| `node-app`           | Node.js/Express HTTP API + MongoDB + OTel SDK (custom MongoDB instrumentation) |
| `go-app`             | Go/Gin HTTP API + MongoDB driver with `otelmongo` + OTLP trace export |
| `springboot-app`     | Java Spring Boot (Spring Web) + Spring Data MongoDB + OTel Java agent |

Each app inserts a document into its own collection every 5 seconds (wrapped in a
root span) and exposes the same HTTP interface, so traces flow continuously:

| App | HTTP interface | Collection |
|-----|----------------|------------|
| `node-app`       | http://localhost:8080 | `events` |
| `go-app`         | http://localhost:8081 | `go_events` |
| `springboot-app` | http://localhost:8082 | `springboot_events` |

Endpoints (all three apps):

- `GET /health` — pings MongoDB
- `GET /events` — recent events + count
- `POST /events` — insert an event
- `GET /explain` — runs `find({ source: "<app>" }).explain("executionStats")` and captures the query plan onto a span (Node via the instrumentation `responseHook`; Go and Spring Boot via an explicit `explainFind` helper)

## Usage

```bash
cd docker
cp .env.example .env          # then fill in the Grafana Cloud values
docker compose -f docker-compose-pdc.yaml up --build
```

Stop and remove (keeping data volumes):

```bash
docker compose -f docker-compose-pdc.yaml down
```

## Configuration

All configuration is via `.env` (copy from `.env.example`).

### MongoDB

| Variable | Description | Default |
|----------|-------------|---------|
| `MONGO_ROOT_USERNAME` | Root username created on first container start | `admin` |
| `MONGO_ROOT_PASSWORD` | Root password for the above user | `changeme` |
| `MONGO_DATABASE` | Initial database created; the apps connect to it | `grafana_pdc` |
| `MONGO_PORT` | Host port mapped to MongoDB's container port `27017` | `27017` |

### Grafana Cloud OTLP (Alloy export target)

| Variable | Description | Example |
|----------|-------------|---------|
| `GCLOUD_OTLP_ENDPOINT` | Grafana Cloud OTLP gateway URL that Alloy forwards telemetry to | `https://otlp-gateway-prod-<region>.grafana.net/otlp` |
| `GCLOUD_OTLP_USERNAME` | Grafana Cloud instance/stack ID, used as the OTLP basic-auth username | `000000` |
| `GCLOUD_RW_API_KEY` | Grafana Cloud access-policy token; the basic-auth password for OTLP (and PDC) | `glc_xxxx…` |

> Find these in Grafana Cloud → **Connections → OTLP Endpoint / Configure**.

### Grafana Cloud — direct Mimir/Loki exporters (used by Alloy)

These feed Alloy's native `prometheus.remote_write` (metrics → Mimir) and `loki.write`
(logs → Loki) components — a **direct** export path, separate from the OTLP gateway above.
The basic-auth password for all of them is `GCLOUD_RW_API_KEY`.

| Variable | Description | Example |
|----------|-------------|---------|
| `GCLOUD_MIMIR_ENDPOINT` | Prometheus remote-write URL (Mimir) that Alloy pushes metrics to | `https://prometheus-prod-<n>-<region>.grafana.net/api/prom/push` |
| `GCLOUD_MIMIR_USERNAME` | Basic-auth username (Mimir instance ID) for remote-write | `0000000` |
| `GCLOUD_LOKI_ENDPOINT` | Loki push URL that Alloy sends logs to | `https://logs-prod-<n>.grafana.net/loki/api/v1/push` |
| `GCLOUD_LOKI_USERNAME` | Basic-auth username (Loki instance ID) for `loki.write` | `0000000` |

> Find the endpoints/usernames in Grafana Cloud → **Connections → Data sources →** your Prometheus/Loki stack → **Details**.

### Grafana Cloud PDC agent

| Variable | Description | Default |
|----------|-------------|---------|
| `GCLOUD_PDC_SIGNING_TOKEN` | PDC signing token used to establish the outbound tunnel | _(required)_ |
| `GCLOUD_HOSTED_GRAFANA_ID` | Hosted Grafana (stack) ID | _(required)_ |
| `GCLOUD_PDC_CLUSTER` | PDC cluster/region the agent connects to | _(required)_ |

> Find these in Grafana Cloud → **Connections → Private data source connections → Configuration Details**.

### MongoDB MCP server

| Variable | Description | Default |
|----------|-------------|---------|
| `MDB_MCP_CONNECTION_STRING` | Connection string the MCP server uses; if blank, defaults to the local `mongodb` service | _(blank → local mongo)_ |
| `MDB_MCP_EXTERNALLY_MANAGED_SESSIONS` | Allow HTTP MCP clients to supply their own session id | `true` |
| `MDB_MCP_LOG_LEVEL` | MCP server log level (`info`, `debug`, …) | `info` |
| `MDB_MCP_PORT` | Host port for the MCP HTTP server (`/mcp`) | `3000` |

### Application HTTP ports

| Variable | Description | Default |
|----------|-------------|---------|
| `NODE_APP_PORT` | Host port mapped to `node-app`'s container port `8080` | `8080` |
| `GO_APP_PORT` | Host port mapped to `go-app`'s container port `8080` | `8081` |
| `SPRINGBOOT_APP_PORT` | Host port mapped to `springboot-app`'s container port `8080` | `8082` |

The apps connect to MongoDB at `mongodb://<user>:<pass>@mongodb:27017/<db>?authSource=admin`
and send OTLP to `http://alloy:4318`.
