# Design

Work in progress. To be filled in:

1. Behaviour in each failure situation (withdraw)
- EN: The Wallet returns an error before applying the operation (400/401): the transaction becomes FAILED; the response is 422 INSUFFICIENT_FUNDS or 502 WALLET_ERROR.
- UA: Wallet повертає помилку до застосування операції (400/401): транзакція стає FAILED; відповідь 422 INSUFFICIENT_FUNDS або 502 WALLET_ERROR.
- EN: The Wallet returns an error after applying it (500): the outcome is unknown, so we retry once; if that fails too, the row stays PENDING and the response is 202.
- UA: Wallet повертає помилку після застосування (500): результат невідомий, тож робимо один повтор; якщо й він невдалий, рядок лишається PENDING, відповідь 202.
- EN: The Wallet times out (10 s per call, WALLET_TIMEOUT): handled like a 500 (one retry, then PENDING and 202).
- UA: Wallet не відповідає вчасно (10 с на виклик, WALLET_TIMEOUT): так само, як 500 (один повтор, потім PENDING і 202).
- EN: Our process stops between the Wallet call and the DB write: the PENDING row is committed before the call, so the call is never lost; the provider's retry gets 409 DuplicatedTransaction from the Wallet and completes the transaction.
- UA: Наш процес падає між викликом Wallet і записом у БД: PENDING-рядок закомічено до виклику, тож виклик не губиться; повтор провайдера отримує 409 DuplicatedTransaction від Wallet і завершує транзакцію.
- EN: A provider retries or sends the same request concurrently: the per-player lock serializes the requests, and the providerTransactionId idempotency key gives a replay (200) without a new Wallet call.
- UA: Провайдер повторює запит або шле його одночасно: лок на гравця серіалізує запити, а ключ ідемпотентності providerTransactionId дає повтор (200) без нового виклику Wallet.
- EN: The Wallet restarts and loses its state: its balances are accepted as they are; a mismatch with oldBalance − amount is logged as a warning.
- UA: Wallet перезапускається і втрачає стан: приймаємо його баланси як є; розбіжність з oldBalance − amount логується як попередження.
- EN: The Wallet also forgets its deduplication keys, so retrying a PENDING transaction applies it again; since the restart also reset the balance, the operation is then applied once on the new state. oldBalance taken from our DB is stale until the next Wallet read; the mismatch is logged.
- UA: Wallet також забуває свої ключі дедуплікації, тож повтор PENDING-транзакції застосує її знову; оскільки рестарт скинув і баланс, операція в новому стані буде застосована один раз. oldBalance з нашої БД застарілий до наступного читання Wallet; розбіжність логується.
2. How a PENDING transaction is resolved
- EN: When the provider retries, the Wallet is called again with the same transactionId (as reference) and betId. A 200 or a 409 DuplicatedTransaction makes it COMPLETED, with newBalance = oldBalance − amount for the 409.
- UA: Коли провайдер повторює запит, Wallet викликається знову з тими самими transactionId (як reference) і betId. 200 або 409 DuplicatedTransaction робить її COMPLETED, для 409 newBalance = oldBalance − amount.
- EN: If the provider never retries, the transaction stays PENDING; a background job is not implemented.
- UA: Якщо провайдер не повторить запит, транзакція лишається PENDING; фонову задачу не реалізовано.
3. Retry after a FAILED attempt
- EN: 409 CONFLICT without a Wallet call. FAILED is the final, audited result of that providerTransactionId; a new attempt needs a new providerTransactionId. The Wallet would accept the retry (a 400 does not consume its key), but reusing the key would make one ID mean two outcomes.
- UA: 409 CONFLICT без виклику Wallet. FAILED — остаточний результат цього providerTransactionId, збережений для аудиту; нова спроба потребує нового providerTransactionId. Wallet прийняв би повтор (400 не займає його ключ), але повторне використання ключа означало б, що один ID має два результати.
4. Zero-amount deposit
- Not decided yet.
5. Other assumptions and what I would improve
- EN: The seed file holds precomputed bcrypt hashes. With more time I would hash the passwords at seed time instead (for example, the service creates the demo accounts on first start), so no hashes are kept in the repository.
- UA: Seed-файл містить заздалегідь пораховані bcrypt-хеші. Якби був час, я б хешувала паролі під час seed (наприклад, сервіс створює демо-акаунти при першому старті), щоб у репозиторії не було хешів.
