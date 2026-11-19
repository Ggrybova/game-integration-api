# Decisions

- Standard library only for routing (net/http ServeMux with method patterns, Go 1.22+) and logging (log/slog): no external dependencies for these.

- The schema and seed are plain SQL files run by the Postgres image from /docker-entrypoint-initdb.d on the first start with an empty volume. There is one schema that does not evolve, so a migration tool adds code without benefit; docker compose down -v resets the state (assignment 5.1). In production I would use versioned migrations (e.g. golang-migrate) as a separate step before deploy.

- Configuration lives in .env (not in git); .env.example is copied once with cp .env.example .env before docker compose up.

- The Wallet API key is not written in code: the service reads it from WALLET_API_KEY. The key is public and valid only for the mock (assignment 5.1), so .env.example holds it, labelled as the mock key.

- After a Wallet restart its balances are accepted as they are (the Wallet is the source of truth for balances, assignment 5.1); we do not rebuild them from our transaction history.

- Three identifiers with separate roles: `providerTransactionId` (from the provider) is the idempotency key of WITHDRAW and DEPOSIT and is unique across the system; `transactionId` (ours) identifies every record, CANCEL included, and is sent to the Wallet as `reference`; the Wallet's own dedup key is (operation, `reference`, `betId`). A Cancel has no ID of its own: its key is the `providerTransactionId` of the transaction being cancelled.

- A CANCEL row has provider_transaction_id = NULL and references the cancelled transaction by its provider_transaction_id (storno_transaction_id TEXT UNIQUE REFERENCES transactions (provider_transaction_id)): that is the Cancel's request field, idempotency key and response field (assignment 7.1, 7.4, 8.5). UNIQUE (provider_transaction_id) covers WITHDRAW and DEPOSIT (NULLs are distinct in Postgres), and UNIQUE (storno_transaction_id) allows at most one Cancel per transaction.

- The bet row is created together with the WITHDRAW transaction, before the Wallet call, with status NULL; it becomes PLACED only when the withdraw is COMPLETED. This keeps betId stored before the first Wallet call, so every retry sends the same betId.

- Money is shopspring/decimal (exact decimal arithmetic), never float64. The Wallet returns balances as strings with any number of decimals, so integer cents cannot hold them; decimal also reads and writes Postgres NUMERIC. JSON output is a number (MarshalJSONWithoutQuotes = true), as the assignment requires (7, 7.4).

- Operations of one player are serialized by a per-player lock, held from the idempotency check until the transaction is updated; different players run in parallel. This makes concurrent identical requests see each other's row (9.2), keeps oldBalance/newBalance consistent and removes races between Withdraw, Deposit and Cancel of the same player (9.6). The cost: a player's requests wait for each other, up to two withdraw calls and the balance checks; waiting is bounded by ctx. The lock is in memory, which is enough for the single api instance in docker compose; with several instances it would move to Postgres (advisory lock). It gives no durability: a PENDING row is committed before every Wallet call (9.1).

- oldBalance is the new_balance of the player's latest non-FAILED transaction in our DB; the Wallet is read only when there is no such transaction or it is PENDING. This avoids an extra call to the unreliable Wallet. If the Wallet's newBalance differs from oldBalance − amount, the balance changed outside our service (e.g. a Wallet restart): this is logged as a warning.

- On a retry, a Wallet 409 DuplicatedTransaction means the withdraw was applied earlier, but the response has no balance. The transaction becomes COMPLETED with newBalance = oldBalance − amount, where oldBalance was stored before the first attempt. Reading GET /balance instead would return the balance at resolution time, which may already include later operations of the player. The cost: if the balance changed outside our service before the first attempt, this value is not exact.

- If the Wallet applied a withdraw but the DB update fails, the API answers 202 PENDING rather than 500: that is the state we recorded, and the provider's retry resolves it by the balance check (DESIGN.md, section 2).

- A retry that resolves a PENDING withdraw to COMPLETED answers 201, not 200: the operation is completed by this request, and there was no completed result to replay.

- A providerTransactionId already used by another player answers 404 NOT_FOUND (the transaction belongs to another player, 7.3), also when two players race and the UNIQUE constraint catches it, so the answer does not depend on who came first.

- DATABASE_URL and WALLET_URL are set in docker-compose.yml, not in .env: they depend on the compose service names (db, wallet), not on the user's setup; the database credentials still come from .env.

- The Wallet call timeout is 10 s: the mock answers in up to ~9 s, and a shorter timeout would turn calls the Wallet completes into PENDING. The cost: with two withdraw calls and up to six balance reads, a request can wait about 80 s in the worst case; the waiting is bounded (9.5).

- The "latest transaction" for oldBalance is ordered by created_dt, not updated_dt: a PENDING withdraw resolved later stores oldBalance − amount computed before newer transactions, while a newer transaction already holds the balance the Wallet returned after it.

- The Wallet client treats any answer it does not recognise (500, an unexpected status, an unreadable 200, a timeout or a dropped connection) as an unknown outcome, never as a rejection: a wrong FAILED records money as not taken although it was, while a wrong PENDING is only resolved later (see the balance check below).

- After an unknown Wallet outcome we read the balance instead of retrying at once: the Wallet checks the balance and currency before its duplicate key, so a retry of an applied withdraw can get 400, and it has no endpoint to ask about one operation. Under the player lock only this transaction changes the balance, so oldBalance − amount means applied, oldBalance means not applied, anything else stays PENDING. Rejected: a zero-amount withdraw with the same key as a probe, because it relies on a quirk and creates Wallet operations nobody asked for.
