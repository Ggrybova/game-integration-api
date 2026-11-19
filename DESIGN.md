# Design

## Scope of this submission

Implemented: login with JWT, Withdraw (bet placement) with idempotency, the three Wallet outcomes, PENDING resolution and concurrency safety; Postgres schema and seed; docker compose; tests with a fake Wallet. Not implemented in the time available: Deposit, Cancel and GET /api/v1/player. The sections below describe Withdraw.

## 1. Behaviour in each failure situation (withdraw)

- The Wallet returns an error before applying the operation (400/401): the transaction becomes FAILED; the response is 422 INSUFFICIENT_FUNDS or 502 WALLET_ERROR.
- The Wallet returns an error after applying it (500): the outcome is unknown, so we read the Wallet balance. oldBalance − amount means it was applied (COMPLETED, 201); oldBalance means it was not, so we retry once with the same transactionId and betId; any other balance, or no balance, leaves the row PENDING and the response is 202.
- The Wallet times out (10 s per call, WALLET_TIMEOUT): handled like a 500 (balance check, at most one retry, then PENDING and 202).
- Our process stops between the Wallet call and the DB write: the PENDING row is committed before the call, so the call is never lost; the provider's retry resolves it as in section 2.
- A provider retries or sends the same request concurrently: the per-player lock serializes the requests, and the providerTransactionId idempotency key gives a replay (200) of a completed transaction without a Wallet call; a PENDING one is resolved as in section 2.
- The Wallet restarts and loses its state: its balances are accepted as they are; a mismatch with oldBalance − amount is logged as a warning.
- The Wallet also forgets its deduplication keys. A PENDING transaction is not simply sent again: the balance check of section 2 usually matches neither oldBalance nor oldBalance − amount after the reset, so it stays PENDING. oldBalance taken from our DB is stale until the next Wallet read; the mismatch is logged.

## 2. How a PENDING transaction is resolved

- When the provider retries, we first read the Wallet balance and compare it with the balance stored before the first call: oldBalance − amount makes the transaction COMPLETED without a new withdraw; oldBalance means it was not applied, so the Wallet is called again with the same transactionId (as reference) and betId, and a 200 or a 409 DuplicatedTransaction makes it COMPLETED; any other balance leaves it PENDING.
- If the provider never retries, the transaction stays PENDING; a background job is not implemented.

## 3. Retry after a FAILED attempt

- 409 CONFLICT without a Wallet call. FAILED is the final, audited result of that providerTransactionId; a new attempt needs a new providerTransactionId. The Wallet would accept the retry (a 400 does not consume its key), but reusing the key would make one ID mean two outcomes.

## 4. Zero-amount deposit

- Deposit is not implemented (see the scope above).

## 5. Other assumptions and what I would improve

- The seed file holds precomputed bcrypt hashes. With more time I would hash the passwords at seed time instead (for example, the service creates the demo accounts on first start), so no hashes are kept in the repository.
- The Wallet checks the balance before its duplicate key, so a retry of an applied withdraw can be answered 400. If the first call is still being processed when we read the balance (a timeout), the retry can get 400 and the transaction is recorded FAILED although it was applied. A status endpoint in the Wallet would remove this.

---

# Дизайн (українська версія)

## Обсяг цієї роботи

Реалізовано: логін з JWT, Withdraw (ставка) з ідемпотентністю, трьома результатами Wallet, розв'язанням PENDING і захистом від гонок; схема і seed Postgres; docker compose; тести з fake Wallet. Не реалізовано за відведений час: Deposit, Cancel і GET /api/v1/player. Розділи нижче описують Withdraw.

## 1. Поведінка в кожній ситуації збою (withdraw)

- Wallet повертає помилку до застосування операції (400/401): транзакція стає FAILED; відповідь 422 INSUFFICIENT_FUNDS або 502 WALLET_ERROR.
- Wallet повертає помилку після застосування (500): результат невідомий, тож читаємо баланс у Wallet. oldBalance − amount означає, що операцію застосовано (COMPLETED, 201); oldBalance означає, що ні, і ми робимо один повтор з тими самими transactionId і betId; будь-який інший баланс або відсутність балансу лишають рядок PENDING, відповідь 202.
- Wallet не відповідає вчасно (10 с на виклик, WALLET_TIMEOUT): так само, як 500 (перевірка балансу, не більше одного повтору, потім PENDING і 202).
- Наш процес падає між викликом Wallet і записом у БД: PENDING-рядок закомічено до виклику, тож виклик не губиться; повтор провайдера розв'язує його, як описано в пункті 2.
- Провайдер повторює запит або шле його одночасно: лок на гравця серіалізує запити, а ключ ідемпотентності providerTransactionId дає повтор (200) завершеної транзакції без виклику Wallet; PENDING-транзакція розв'язується, як описано в пункті 2.
- Wallet перезапускається і втрачає стан: приймаємо його баланси як є; розбіжність з oldBalance − amount логується як попередження.
- Wallet також забуває свої ключі дедуплікації. PENDING-транзакцію не надсилаємо просто так ще раз: після скидання перевірка балансу з пункту 2 зазвичай не збігається ні з oldBalance, ні з oldBalance − amount, тож вона лишається PENDING. oldBalance з нашої БД застарілий до наступного читання Wallet; розбіжність логується.

## 2. Як розв'язується PENDING-транзакція

- Коли провайдер повторює запит, спершу читаємо баланс у Wallet і порівнюємо його з балансом, збереженим перед першим викликом: oldBalance − amount робить транзакцію COMPLETED без нового withdraw; oldBalance означає, що її не застосовано, тож Wallet викликається знову з тими самими transactionId (як reference) і betId, і 200 або 409 DuplicatedTransaction робить її COMPLETED; будь-який інший баланс лишає її PENDING.
- Якщо провайдер не повторить запит, транзакція лишається PENDING; фонову задачу не реалізовано.

## 3. Повтор після спроби, що закінчилась FAILED

- 409 CONFLICT без виклику Wallet. FAILED — остаточний результат цього providerTransactionId, збережений для аудиту; нова спроба потребує нового providerTransactionId. Wallet прийняв би повтор (400 не займає його ключ), але повторне використання ключа означало б, що один ID має два результати.

## 4. Deposit на нульову суму

- Deposit не реалізовано (див. обсяг вище).

## 5. Інші припущення і що я б покращила

- Seed-файл містить заздалегідь пораховані bcrypt-хеші. Якби був час, я б хешувала паролі під час seed (наприклад, сервіс створює демо-акаунти при першому старті), щоб у репозиторії не було хешів.
- Wallet перевіряє баланс раніше за свій ключ дедуплікації, тож на повтор уже застосованого withdraw він може відповісти 400. Якщо перший виклик ще обробляється, коли ми читаємо баланс (таймаут), повтор може отримати 400, і транзакцію буде записано FAILED, хоча її застосовано. Ендпоінт статусу операції у Wallet усунув би це.
