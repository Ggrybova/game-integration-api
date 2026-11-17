# Decisions

- Packages are named by what they contain (betting, postgres, wallet, auth, httpapi, config): short, no underscores. The module follows the go.dev server layout (cmd/ + internal/). The business package `betting` owns the Repository and Wallet interfaces, so it imports nothing from the adapters.
- Standard library only for routing (net/http ServeMux with method patterns, Go 1.22+) and logging (log/slog): no external dependencies for these.
- Start with a single main.go and no config package: configuration and extra layers are added only when there is something to put in them.
- Multi-stage Dockerfile: the Go toolchain is only in the build stage, the runtime image holds a static binary and runs as a non-root user.
- docker-compose runs the api and the mock Wallet on one network; the api will reach the Wallet by service name (http://wallet:8000). The database and .env.example are added together with the first code that needs them.
- The Wallet API key is not written in code or docs: the service reads it from `WALLET_API_KEY`. The key is public and valid only for the mock (assignment 5.1), so when `.env.example` appears it will hold the key once, labelled as the mock key, to keep `docker compose up` a single command.
- Base images are pinned to the Alpine minor version (`golang:1.27.4-alpine3.24`, `alpine:3.24`), the same in both stages: no unexpected jump to a new Alpine release, while patch updates still arrive.
