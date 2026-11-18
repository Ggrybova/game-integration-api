# Withdraw flow

## EN

1. Validate the fields themselves: currency is present and is one of the three values, amount is greater than zero with at most two decimals, providerTransactionId is not empty
2. Take the player lock.
3. Check the input data. By provider_transaction_id get user_id, amount, type, status from the DB (currency comes from the player's account)
    if the player is different -> 404 NOT_FOUND
    if user_id, currency, amount are the same, type = WITHDRAW, status = PENDING -> go to step 10
    if user_id, currency, amount are the same, type = WITHDRAW, status = FAILED -> 409 CONFLICT
    if user_id, currency, amount are the same, type = WITHDRAW, status = CANCELLED -> 409 CONFLICT
    if user_id, currency, amount are the same, type = WITHDRAW -> 200 replay
    if user_id, currency, amount are the same, type = DEPOSIT -> (the key is already taken by another operation) 409 CONFLICT
    if currency or amount differ -> 409 CONFLICT
    if there is no record, go on
4. Check the user's currency: by user_id get currency and the current balance from the DB (it is needed for the response; if it is not there, read the balance from the Wallet)
    if the currency is different -> 400 INVALID_REQUEST
    if the currency matches -> go on
5. Write to the DB in one transaction
    5.1. bets:
        id (generated automatically) | status null according to 7.4
    5.2. Generate transactionId
    transactions:
        transactionId | providerTransactionId | bet_id | user_id | amount | old_balance | type | status (PENDING)
    if UNIQUE on provider_transaction_id fails (another player took the key at the same time) -> 404 NOT_FOUND
6. Call the external API and log it (9.7), at most two withdraw calls
    500 Internal error or no response, connection dropped (~5 s) -> go to step 10
    400, 401 -> FAILED, return an error (InsufficientBalance -> 422, others -> 502)
    409 {"msg":"Transaction already exists.","code":"DuplicatedTransaction"} -> newBalance = oldBalance − amount -> go on
    200 -> get the new balance -> go on
7. Record the result
    if the new balance ≠ oldBalance − amount → warning in the log
    update the statuses in the bets and transactions tables
    -> 201 (also when this request resolves an earlier PENDING)
    if the DB update fails, the row stays PENDING -> 202
8. Release the player lock.
9. Response.

10. Ask the Wallet for the balance /api/v1/balance/{user_id}, at most two balance calls ->
    if it returned 200 with balance ->
        check whether the money was taken ->
            if yes (balance = oldBalance − amount) -> newBalance = balance -> go to step 7
            if no (balance = oldBalance) -> go to step 6 (if no withdraw call is left, the row stays PENDING -> 202)
            otherwise -> PENDING -> 202
    if it returned an error or the connection dropped -> retry step 10 once more
    when the attempts are used up, the row stays PENDING -> 202

## UA

1. Валідація самих полів, типу є currency, що має одне з трьох значень, amount більше нуля і не більше двох знаків після коми, providerTransactionId не пустий
2. Взяти лок на гравця.
3. Перевірка вхідних даних. За provider_transaction_id дістаємо з бд user_id, amount, type, status (currency беремо з акаунта гравця)
    якщо гравець інший -> 404 NOT_FOUND
    якщо user_id, currency, amount ті самі, type = WITHDRAW, status = PENDING -> йдемо до пункту 10
    якщо user_id, currency, amount ті самі, type = WITHDRAW, status = FAILED ->  409 CONFLICT
    якщо user_id, currency, amount ті самі, type = WITHDRAW, status = CANCELLED ->  409 CONFLICT
    якщо user_id, currency, amount ті самі, type = WITHDRAW -> 200 повтор
    якщо user_id, currency, amount ті самі, type = DEPOSIT -> (ключ уже зайнятий іншою операцією) 409 CONFLICT
    якщо currency чи amount -> 409 CONFLICT
    якщо нема запису йдемо далі
4. Перевірка  currency користувача: За user_id дістаємо з бд currency та поточний balance (він потрібен для відповіді, якщо його немає читаємо баланс з Wallet)
    якщо currency інший -> 400 INVALID_REQUEST
    якщо currency збігається -> йдемо далі
5. Запис до бд у транзакції
    5.1. bets:
        id (сам генерується) | status null відповідно до 7.4
    5.2. Генеруємо transactionId
    transactions:
        transactionId | providerTransactionId | bet_id | user_id | amount | old_balance | type | status (PENDING)
    якщо спрацював UNIQUE на provider_transaction_id (інший гравець одночасно зайняв ключ) -> 404 NOT_FOUND
6. Виклик external API та логування (9.7), не більше двох викликів withdraw
    500 Internal error чи no response Connection dropped (~5 s) -> йдемо до пункту 10
    400, 401 -> FAILED, вертаємо помилку (InsufficientBalance -> 422, інші -> 502)
    409 {"msg":"Transaction already exists.","code":"DuplicatedTransaction"} -> newBalance = oldBalance − amount -> йдемо далі
    200 -> отримуємо новий баланс -> йдемо далі
7. Фіксація результату
    якщо новий баланс ≠ oldBalance − amount → попередження в лог
    оновлюємо статуси у таблицях bets, transactions
    -> 201 (і тоді, коли цей запит розв'язує попередній PENDING)
    якщо оновлення БД не вдалося, рядок лишається PENDING -> 202
8. Зняти лок на гравця.
9. Відповідь.

10. Питаємо баланс у Wallet /api/v1/balance/{user_id}, не більше двох викликів balance ->
    якщо повернулось 200 з balance -> 
        перевіряємо чи списались гроші ->
            якщо так -> йдемо до пункту 7
            якщо ні -> йдемо до пункту 6
            інакше → PENDING → 202 
    якщо повернулась помилка чи був обрив -> пробуємо повторити пункт 10 ще раз 
    при вичерпанні спроб йдемо до пункту 7 рядок лишається PENDING -> 202
