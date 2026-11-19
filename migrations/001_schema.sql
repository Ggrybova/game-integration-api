CREATE TABLE accounts
(
    user_id       BIGINT PRIMARY KEY,   -- wallet user id, 64-bit (5.2)
    username      TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL, -- bcrypt hash, never the password
    currency      TEXT        NOT NULL CHECK (currency IN ('USD', 'EUR', 'KES')),
    created_dt    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE bets
(
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, -- betId sent to the Wallet
    status     TEXT CHECK (status IN ('PLACED', 'WON', 'LOST', 'CANCELLED')),
    -- NULL until the withdraw is COMPLETED
    created_dt TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_dt TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE transactions
(
    id                      UUID PRIMARY KEY, -- our transactionId, sent as Wallet reference
    type                    TEXT           NOT NULL CHECK (type IN ('WITHDRAW', 'DEPOSIT', 'CANCEL')),
    status                  TEXT           NOT NULL CHECK (status IN ('PENDING', 'COMPLETED', 'FAILED', 'CANCELLED')),
    user_id                 BIGINT         NOT NULL REFERENCES accounts (user_id),
    bet_id                  BIGINT         NOT NULL REFERENCES bets (id),
    amount                  NUMERIC(20, 2) NOT NULL CHECK (amount >= 0),
    provider_transaction_id TEXT UNIQUE,      -- NULL for CANCEL
    storno_transaction_id   TEXT UNIQUE REFERENCES transactions (provider_transaction_id),
    -- set only for CANCEL
    wallet_transaction_id   BIGINT,           -- Wallet's id, for reference only
    old_balance             NUMERIC,          -- exactly as the Wallet returned it
    new_balance             NUMERIC,          -- NULL while PENDING
    created_dt              TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_dt              TIMESTAMPTZ    NOT NULL DEFAULT now(),
    -- a CANCEL has no provider id and points to the cancelled row; others the opposite
    CHECK ((type = 'CANCEL') = (provider_transaction_id IS NULL AND storno_transaction_id IS NOT NULL))
);

-- one withdraw per bet
CREATE UNIQUE INDEX one_withdraw_per_bet ON transactions (bet_id) WHERE type = 'WITHDRAW';

-- one active settlement per bet (8.4); FAILED and CANCELLED deposits do not count
CREATE UNIQUE INDEX one_active_deposit_per_bet ON transactions (bet_id) WHERE type = 'DEPOSIT' AND status IN ('PENDING', 'COMPLETED');
