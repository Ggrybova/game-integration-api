# Game Integration API

A Go service that places and settles casino bets on behalf of players,
on top of an unreliable Wallet service (KenTech backend assessment).

Implemented: `POST /api/v1/auth/login`, `GET /api/v1/player` and `POST /api/v1/withdraw`.
Not implemented yet: deposit and cancel (see [DESIGN.md](DESIGN.md)).

## Run

Everything in Docker (the service, Postgres and the mock Wallet):

```bash
cp .env.example .env    # then put your own random JWT_SECRET in .env
docker compose up --build
```

Health check: `curl localhost:8080/health`

The service on the host, Postgres and the Wallet in Docker
(useful while changing code, no image build):

```bash
docker compose stop api                 # frees port 8080
docker compose up -d db wallet
set -a; source .env; set +a
DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@localhost:5432/$POSTGRES_DB?sslmode=disable" \
WALLET_URL=http://localhost:8000 \
go run ./cmd/api
```

Reset all state: `docker compose down -v` removes the database volume and
the containers; the Wallet keeps its state in memory, so it starts again
with the initial balances.

Observed behaviour of the mock Wallet: [docs/wallet_api.md](docs/wallet_api.md).

## Try it

```bash
# Log in; the token is kept in TOKEN
TOKEN=$(curl -s -X POST localhost:8080/api/v1/auth/login \
  -d '{"username":"player1","password":"password1"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')

# The player and the current balance: 200, or 502/504 if the balance cannot be read
curl -s -w ' [%{http_code}]\n' localhost:8080/api/v1/player -H "Authorization: Bearer $TOKEN"

# Place a bet: 201, or 202 if the Wallet outcome is unknown
curl -s -w ' [%{http_code}]\n' -X POST localhost:8080/api/v1/withdraw \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"currency":"USD","amount":10.50,"providerTransactionId":"my-1"}'
```

Repeat the same request: 200 (a replay without a Wallet call), or 201 if it
resolves an earlier 202. The same `providerTransactionId` with another
amount gives 409; an amount the balance does not cover gives 422 and a
FAILED record (the Wallet never lets the balance reach zero, so withdrawing
the whole balance gives 422 too); another currency gives 400; no token gives 401.

Inspect the result:

```bash
# Our transactions
docker compose exec db psql -U game -c \
  "select provider_transaction_id, status, bet_id, amount, old_balance, new_balance from transactions order by created_dt"

# The current balance; it should equal the latest new_balance
curl -s -w ' [%{http_code}]\n' localhost:8080/api/v1/player -H "Authorization: Bearer $TOKEN"

# Every Wallet call the service made for this bet
docker compose logs api | grep my-1
```

### Calling the Wallet directly

The mock Wallet runs on port 8000 and needs the `x-api-key` header:

```bash
KEY=$(grep '^WALLET_API_KEY=' .env | cut -d= -f2)

# Balance of a wallet user
curl -s -w ' [%{http_code}]\n' -H "x-api-key: $KEY" localhost:8000/api/v1/balance/34633089486

# Withdraw; change betId and reference for every new operation
curl -s -w ' [%{http_code}]\n' -H "x-api-key: $KEY" -H 'content-type: application/json' \
  -X POST localhost:8000/api/v1/withdraw \
  -d '{"userId":34616761765,"currency":"KES","transactions":[{"amount":1.50,"betId":1,"reference":"t-1"}]}'
```

`[000]` or `[500]` is the mock being unreliable: repeat the same command
(the same `betId` and `reference` are never applied twice). A direct withdraw
bypasses our service, so our database does not know about it; reset the state
afterwards with `docker compose down -v`.

## Environment variables

Set in `.env` (copied from `.env.example`):

| Variable | Example | Meaning |
|---|---|---|
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | `game` | Database name and credentials |
| `WALLET_API_KEY` | mock key | Wallet API key (assignment 5.1) |
| `WALLET_TIMEOUT` | `10s` | Timeout of one Wallet call |
| `JWT_SECRET` | random, 32+ bytes | JWT signing key; use your own value |
| `JWT_TTL` | `1h` | Token lifetime |

Set in `docker-compose.yml`, because they use the compose service names:
`DATABASE_URL` (`postgres://...@db:5432/...`) and `WALLET_URL` (`http://wallet:8000`).

## Tests

Requires Go 1.27.4. The business-rule tests use a fake Wallet and a fake
repository, so no Docker, database or Wallet is needed.

```bash
go test -race ./...
```

`-race` enables the data race detector; add `-v` to see each test.

The Postgres tests run only when `TEST_DATABASE_URL` is set; each test
creates a fresh `game_test` database from `migrations/`, so the `game`
database is not touched:

```bash
docker compose up -d db
set -a; source .env; set +a
TEST_DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@localhost:5432/$POSTGRES_DB?sslmode=disable" go test -race ./...
```

## Demo accounts

Seeded by `migrations/002_seed.sql`, one per Wallet user:

| Username | Password  | Wallet user ID | Currency |
|----------|-----------|----------------|----------|
| player1  | password1 | 34633089486    | USD      |
| player2  | password2 | 34679664254    | EUR      |
| player3  | password3 | 34616761765    | KES      |
| player4  | password4 | 34673635133    | USD      |

## Use of AI tools

I used Claude Code as an assistant for reviewing design decisions,
explaining Go idioms and writing Go code and tests.
