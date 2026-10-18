# SQLite Lite: from a fresh checkout to your first Relay request

[简体中文](./quickstart-lite.md) | [English](./quickstart-lite.en.md)

Lite keeps all nine application services and Redis, replacing MySQL with SQLite
files in a Docker volume. It is intended for single-host, low-concurrency use.
The existing [smoke test](../scripts/test-lite-smoke.py) starts the services,
logs in over HTTP, creates a channel, funds a test wallet, creates a token,
lists models, sends a chat request, checks restart persistence and restores a backup into empty volumes.
Only the upstream model is mocked; no provider credentials are needed for that test.

Initial dependency downloads and source compilation take additional time. Any
“10-minute setup” estimate starts after images are ready. Use v0.27.0 or newer;
the Compose fixes described here are absent from v0.26.6 and earlier.

## 1. Prepare and start

Install Git, Docker Engine or Docker Desktop with the Compose plugin, and
Python 3 for key generation. Local Go, Node.js and MySQL installations are not
required; Docker builds the application.

Allocate at least **4 CPUs, 8 GiB RAM and 20 GiB free disk** to Docker for the
initial source build. A runtime budget of 2 CPUs / 2 GiB is a starting estimate
for light use, not a tested minimum capacity guarantee. SQLite is unsuitable
for database files shared across hosts or highly concurrent writes.

```bash
git clone https://github.com/mengbin92/micro-one-api.git
cd micro-one-api/deployments/docker-compose
cp .env.lite.example .env
python3 - <<'PY'
from pathlib import Path
import secrets
p = Path('.env')
values = {
    'JWT_SECRET_KEY': secrets.token_hex(32),
    'SERVICE_TOKEN': secrets.token_hex(32),
    'ADMIN_TOKEN': secrets.token_hex(32),
    # The AES key is the literal string: 16 random bytes become 32 ASCII bytes.
    'CHANNEL_ENCRYPTION_KEY': secrets.token_hex(16),
    'REDIS_PASSWORD': secrets.token_hex(32),
}
p.write_text('\n'.join(
    f'{line.split("=", 1)[0]}={values[line.split("=", 1)[0]]}'
    if '=' in line and line.split('=', 1)[0] in values else line
    for line in p.read_text().splitlines()
) + '\n')
p.chmod(0o600)
PY
python3 ../../scripts/prepare-rbac-service-identities.py --output .env.service-identities
cat .env.service-identities >> .env
python3 ../../scripts/check-service-identities.py --env .env
rm .env.service-identities

# Run subsequent commands in this directory. Redefine this in each new terminal.
lite() { docker compose -p oneapi-lite --env-file .env -f docker-compose.lite.yml "$@"; }
lite config --quiet
# Build services serially. COMPOSE_PARALLEL_LIMIT alone does not limit BuildKit compilation.
for service in migrate identity-service notify-worker channel-service billing-service config-service log-service monitor-worker relay-gateway admin-api; do
  lite build "$service"
done
lite up -d
lite ps -a
lite logs migrate
```

Dedicated service identities and receiver trust maps are required for execution
admission; the shared `SERVICE_TOKEN` cannot replace them. The existing tool
derives these credentials from the RPC policies without switching IAM mode or
expanding rollouts. Preserve them on upgrades.

`sqlite-init` and `migrate` should exit with code 0. Redis should be healthy and
the application services should remain running. `sqlite-init` sets volume
permissions; migrations and services access SQLite as UID 65534. Later starts
check and apply pending migrations.

| Endpoint | Default address | `.env` override if the port is busy |
|---|---|---|
| Console | `http://127.0.0.1:3000` | `LITE_ADMIN_PORT=13000` |
| Relay | `http://127.0.0.1:8080` | `LITE_RELAY_PORT=18080` |
| Health | `http://127.0.0.1:8080/healthz` | Use the Relay port |

Services bind to `127.0.0.1` by default. Use SSH forwarding for a remote trial.
For public access, configure an HTTPS reverse proxy and set `LITE_BIND_ADDRESS`
explicitly. Redis and internal services do not expose host ports. Give separate
instances distinct Compose project names and host ports.

## 2. Log in and create a channel

The first start creates `admin` if the user table is empty. Its random password
is written to a private file inside the SQLite volume, not to service logs.
Read it in your own terminal and store it in a password manager:

```bash
lite exec -T identity-service cat /data/initial-admin-password.txt
```

Alternatively, set `INITIAL_ADMIN_PASSWORD` in `.env` **before the first start**;
then no password file is generated. Changing this variable does not reset an
existing account. After changing the password, you can delete the initial
password file. Recovery uses the [admin reset tool](../cmd/admin-reset/main.go).

Open the console, log in and switch to English using the language control.
Open **Admin → Channels** and create a channel:

| Field | Value |
|---|---|
| Name | For example, `my-provider` |
| Provider | Select OpenAI for an OpenAI-compatible upstream |
| Base URL | Your provider's API root, such as `https://api.example.com/v1`; omit `/chat/completions` |
| API key | Your upstream provider API key |
| Models | Actual supported model IDs; follow the form's separator instructions |
| Group | `default`, matching the current user's group |
| Priority / weight | Keep the defaults initially |

Save and test the channel. A real upstream test consumes provider quota. If it
fails, check the URL, model IDs, credentials and upstream account balance.
Inside a container, `localhost` refers to that container, not the host's model
server. The smoke test permits its private mock only through a temporary
override; normal configurations retain SSRF checks.

## 3. Fund a wallet and create a token

Check the calling user's wallet on the Dashboard. The initial admin has test
credit by default. If more is needed, create a redemption code under the Admin
redemption-code page: use an amount such as `1.00` and quantity `1`. Copy the
generated code, redeem it on the user redemption page, then verify the balance.
This credits the local wallet; the upstream provider balance is separate.
An unlimited API token removes the token's own quota limit, not wallet charges.

Open the API token page and create a token. Save the complete value immediately.
The channel's upstream key and the client's Relay token are different credentials.

In a **Bash terminal**, read the token without placing it in command history:

```bash
read -r -s -p 'Relay Token: ' API_TOKEN
printf '\n'
export API_TOKEN
export RELAY_URL=http://127.0.0.1:8080
export MODEL='<actual model ID configured on your channel>'
curl --fail-with-body -H "Authorization: Bearer ${API_TOKEN}" \
  "${RELAY_URL}/v1/models"
curl --fail-with-body "${RELAY_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H 'Content-Type: application/json' \
  --data "{\"model\":\"${MODEL}\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}],\"max_tokens\":16,\"stream\":false}"
unset API_TOKEN
```

You should receive a model list followed by `choices[0].message.content` and
`usage`. Check the request in Usage Records. If the wallet is insufficient,
add credit before retrying.

The browser Playground is optional. For cross-origin access, add the console's
**exact origin**, such as `http://127.0.0.1:3000`, to `CORS_ALLOWED_ORIGINS`,
then recreate the Relay container.

## 4. Save keys, back up and restore

`oneapi-lite_sqlite_data` contains SQLite databases, WAL/SHM files and the initial
password file. Redis uses `oneapi-lite_redis_data`. Keep `.env`, especially
`CHANNEL_ENCRYPTION_KEY`, with your database backup: losing that key prevents
channel credential decryption. Preserve existing keys across restarts and upgrades.
Do not commit `.env`, backups or tokens.

For a low-concurrency deployment, use a short maintenance window. Pause client
traffic and wait for active requests and asynchronous billing work to finish:

```bash
umask 077
backup_dir="$(pwd)/backup-$(date +%Y%m%d-%H%M%S)"
mkdir "$backup_dir"
lite stop
lite run --rm -T --no-deps sqlite-init tar -C /data -czf - . > "$backup_dir/sqlite.tar.gz"
lite run --rm -T --no-deps redis tar -C /data -czf - . > "$backup_dir/redis.tar.gz"
cp .env "$backup_dir/env"
git rev-parse HEAD > "$backup_dir/revision.txt"
lite up -d
```

Copy backups to another disk or host. Do not copy only a live `.db` file.
`down -v` deletes persistent volumes. Verify recovery using another project
name, empty volumes and unused ports after your first backup.

To restore, use the recorded revision, saved `.env` and **empty volumes**. Define
`lite` for the recovery project and set `backup_dir` to your backup directory.
Before starting application services:

```bash
lite run --rm -T --no-deps sqlite-init tar -C /data -xzf - < "$backup_dir/sqlite.tar.gz"
lite run --rm -T --no-deps redis tar -C /data -xzf - < "$backup_dir/redis.tar.gz"
lite up -d
```

Check login, channels and existing tokens. Never restore over actively written
volumes or combine SQLite and Redis backups from different points in time.

## 5. Upgrade boundaries

Read the target release notes and make a consistent backup first:

```bash
lite stop
# Switch the repository to the reviewed target tag/commit; preserve .env.
for service in migrate identity-service notify-worker channel-service billing-service config-service log-service monitor-worker relay-gateway admin-api; do
  lite build "$service"
done
lite run --rm migrate
lite up -d
lite logs migrate
```

Lite bundles the frontend at `/web` in the `admin-api` image. Main production
Compose uses a separate host `/opt/web/dist` mount. Follow the target release's
rollback instructions; after schema changes, use the consistent database/Redis
backup and matching application revision rather than a destructive down migration.

SQLite/PostgreSQL migration `011` adds missing channel columns; `090` repairs
historical billing timestamp types. SQLite `090` rebuilds affected tables in a
transaction while preserving amounts, IDs, sequences and indexes. Stop services,
back up, and reserve enough disk for an additional copy of affected tables.
These dialect-specific migrations do not change MySQL schemas.

Executor and routing-group rollout switches remain off by default in Lite;
canonical usage stays in observe mode. This guide does not enable charging
rollouts or historical reversals. Routing-group migrations 092–100 are applied
by the Lite migration container. Optional rollout details are in the
[routing-group runbook](./runbooks/routing-groups-runbook.md) (Chinese); use the
SQLite dialect (`--driver=sqlite3`) without per-schema ownership steps.

## 6. Repeatable smoke test

From the repository root:

```bash
python3 scripts/test-lite-smoke.py
```

The script copies tracked source to a fresh temporary directory, generates test
secrets, uses a unique project name, random loopback ports and new volumes, and
generates protobuf inputs during image builds. It does not read your `.env`,
reuse production volumes or call external model providers. Successful checks
print PASS; containers, images, networks and volumes are cleaned up by default.
It also stops the stores, backs up SQLite, Redis and `.env`, and restores them
into a second project's empty volumes using the same built images. Recovery
checks the original login, wallet, Redis data, Token and encrypted channel credentials.
`--keep` retains the isolated environment and prints cleanup instructions.
Diagnostic logs remain in a private temporary directory and may contain test data.

See the [redacted screenshots](./assets/screenshots) and
[Lite walkthrough](./assets/demos/lite-quickstart.mp4). The walkthrough uses
temporary credentials and a local mock; it is not a production capacity test.
Advanced deployment and operational documents are currently maintained in Chinese.
