# Game Integration API

A Go service that places and settles casino bets on behalf of players,
on top of an unreliable Wallet service (KenTech backend assessment).

## Run

```bash
docker compose up --build
```

Health check: `curl localhost:8080/health`

Environment variables, seeded users, tests and state reset are added
here as the features appear. Notes on the Wallet mock are in
[docs/wallet_api.md](docs/wallet_api.md).

## Use of AI tools

I used Claude Code as an assistant for reviewing design decisions
and explaining Go idioms.
