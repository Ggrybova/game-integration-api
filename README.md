# Game Integration API

A Go service that places and settles casino bets on behalf of players,
on top of an unreliable Wallet service (KenTech backend assessment).

## Run

```bash
cp .env.example .env
docker compose up --build
```

Health check: `curl localhost:8080/health`

Reset all state: `docker compose down -v` removes the database volume;
the Wallet keeps its state in memory and resets when its container restarts.

[docs/wallet_api.md](docs/wallet_api.md).

## Demo accounts

Seeded by `migrations/002_seed.sql`, one per Wallet user:

| Username | Password  | Wallet user ID | Currency |
|----------|-----------|----------------|----------|
| player1  | password1 | 34633089486    | USD      |
| player2  | password2 | 34679664254    | EUR      |
| player3  | password3 | 34616761765    | KES      |
| player4  | password4 | 34673635133    | USD      |

## Use of AI tools

I used Claude Code as an assistant for reviewing design decisions
and explaining Go idioms.
