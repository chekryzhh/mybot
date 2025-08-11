CREATE TABLE IF NOT EXISTS clients (
id SERIAL PRIMARY KEY,
chat_id BIGINT UNIQUE NOT NULL,
username TEXT DEFAULT '',
created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS channels (
                                        id SERIAL PRIMARY KEY,
                                        telegram_channel_id BIGINT NOT NULL,
                                        client_id INTEGER REFERENCES clients(id) ON DELETE CASCADE,
    channel_title TEXT,
    subscription_until TIMESTAMP,
    wallet_address TEXT,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE (client_id, telegram_channel_id)
    );



CREATE TABLE IF NOT EXISTS scheduled_posts (
id SERIAL PRIMARY KEY,
channel_id INTEGER REFERENCES channels(id) ON DELETE CASCADE,
content TEXT,
post_at TIMESTAMP,
theme TEXT,
style TEXT,
language TEXT,
length TEXT,
photo TEXT, -- ✅ Новое поле
created_at TIMESTAMP DEFAULT NOW()
);

-- служебные таблицы TON-воркера
CREATE TABLE IF NOT EXISTS ton_watcher_state (
                                                 wallet TEXT PRIMARY KEY,
                                                 last_utime BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS ton_payments(
                                           hash TEXT PRIMARY KEY,
                                           utime BIGINT NOT NULL,
                                           value TEXT NOT NULL,
                                           source TEXT,
                                           comment TEXT,
                                           processed_at TIMESTAMPTZ DEFAULT now()
    );
