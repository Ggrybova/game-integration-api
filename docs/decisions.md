# Decisions

- EN: Standard library only for routing (net/http ServeMux with method patterns, Go 1.22+) and logging (log/slog): no external dependencies for these.
- UA: Лише стандартна бібліотека для роутингу (net/http ServeMux з методами в шаблонах, Go 1.22+) і логів (log/slog): жодних зовнішніх залежностей для цього.

- EN: The schema and seed are plain SQL files run by the Postgres image from /docker-entrypoint-initdb.d on the first start with an empty volume. There is one schema that does not evolve, so a migration tool adds code without benefit; docker compose down -v resets the state (assignment 5.1). In production I would use versioned migrations (e.g. golang-migrate) as a separate step before deploy.
- UA: Схема і seed — звичайні SQL-файли, які образ Postgres виконує з /docker-entrypoint-initdb.d при першому старті з порожнім volume. Схема одна й не змінюється, тож інструмент міграцій додав би код без користі; docker compose down -v скидає стан (завдання 5.1). У production я б використала версіоновані міграції (наприклад, golang-migrate) окремим кроком перед деплоєм.

- EN: Configuration lives in .env (not in git); .env.example is copied once with cp .env.example .env before docker compose up. No defaults are hidden in docker-compose.yml.
- UA: Конфігурація живе в .env (не в git); .env.example копіюється один раз командою cp .env.example .env перед docker compose up. У docker-compose.yml немає прихованих значень за замовчуванням.

- EN: The Wallet API key is not written in code: the service reads it from WALLET_API_KEY. The key is public and valid only for the mock (assignment 5.1), so .env.example holds it, labelled as the mock key.
- UA: Ключ Wallet API не записано в коді: сервіс читає його з WALLET_API_KEY. Ключ публічний і діє лише для mock (завдання 5.1), тому .env.example містить його з позначкою, що це ключ mock.

- EN: After a Wallet restart its balances are accepted as they are (the Wallet is the source of truth for balances, assignment 5.1); we do not rebuild them from our transaction history.
- UA: Після рестарту Wallet приймаємо його баланси як є (Wallet є джерелом істини для балансів, завдання 5.1); не відновлюємо їх з нашої історії транзакцій.

- EN: Three identifiers with separate roles: `providerTransactionId` (from the provider) is the idempotency key of WITHDRAW and DEPOSIT and is unique across the system; `transactionId` (ours) identifies every record, CANCEL included, and is sent to the Wallet as `reference`; the Wallet's own dedup key is (operation, `reference`, `betId`). A Cancel has no ID of its own: its key is the `providerTransactionId` of the transaction being cancelled.
- UA: Три ідентифікатори з різними ролями: `providerTransactionId` (від провайдера) є ключем ідемпотентності WITHDRAW і DEPOSIT і унікальний у всій системі; `transactionId` (наш) ідентифікує кожен запис, зокрема CANCEL, і надсилається у Wallet як `reference`; власний ключ дедуплікації Wallet — (операція, `reference`, `betId`). Cancel не має власного ID: його ключ — `providerTransactionId` транзакції, яку скасовують.

- EN: A CANCEL row has provider_transaction_id = NULL and references the cancelled transaction by its provider_transaction_id (storno_transaction_id TEXT UNIQUE REFERENCES transactions (provider_transaction_id)): that is the Cancel's request field, idempotency key and response field (assignment 7.1, 7.4, 8.5). UNIQUE (provider_transaction_id) covers WITHDRAW and DEPOSIT (NULLs are distinct in Postgres), and UNIQUE (storno_transaction_id) allows at most one Cancel per transaction.
- UA: Рядок CANCEL має provider_transaction_id = NULL і посилається на скасовану транзакцію через її provider_transaction_id (storno_transaction_id TEXT UNIQUE REFERENCES transactions (provider_transaction_id)): це поле запиту Cancel, його ключ ідемпотентності і поле відповіді (завдання 7.1, 7.4, 8.5). UNIQUE (provider_transaction_id) покриває WITHDRAW і DEPOSIT (у Postgres NULL не рівні між собою), а UNIQUE (storno_transaction_id) дозволяє не більше одного Cancel на транзакцію.

- EN: The bet row is created together with the WITHDRAW transaction, before the Wallet call, with status NULL; it becomes PLACED only when the withdraw is COMPLETED. This keeps betId stored before the first Wallet call, so every retry sends the same betId.
- UA: Рядок ставки створюється разом із транзакцією WITHDRAW, до виклику Wallet, зі статусом NULL; PLACED він стає лише після COMPLETED withdraw. Так betId збережено до першого виклику Wallet, і кожен повтор надсилає той самий betId.
