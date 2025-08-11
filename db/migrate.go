package db

import (
	"database/sql"
	"log"
)

func RunMigrations(db *sql.DB) {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS clients (
			id SERIAL PRIMARY KEY,
			chat_id BIGINT UNIQUE NOT NULL,
			username TEXT DEFAULT '',
			created_at TIMESTAMP DEFAULT NOW()
		);`,

		`CREATE TABLE IF NOT EXISTS channels (
			id SERIAL PRIMARY KEY,
			telegram_channel_id BIGINT NOT NULL,
			client_id INTEGER REFERENCES clients(id) ON DELETE CASCADE,
			channel_title TEXT,
			wallet_address TEXT,
			is_active BOOLEAN DEFAULT TRUE,
			subscription_until TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			UNIQUE (client_id, telegram_channel_id)
		);`,

		`CREATE TABLE IF NOT EXISTS scheduled_posts (
			id SERIAL PRIMARY KEY,
			channel_id INTEGER REFERENCES channels(id) NOT NULL,
			content TEXT,
			post_at TIMESTAMP,
			theme TEXT,
			style TEXT,
			language TEXT,
			length TEXT,
			photo TEXT,
			created_at TIMESTAMP DEFAULT NOW()
		);`,

		`CREATE TABLE IF NOT EXISTS ton_watcher_state (
			wallet TEXT PRIMARY KEY,
			last_utime BIGINT NOT NULL DEFAULT 0
		);`,

		`CREATE TABLE IF NOT EXISTS ton_payments(
			hash TEXT PRIMARY KEY,
			utime BIGINT NOT NULL,
			value TEXT NOT NULL,
			source TEXT,
			comment TEXT,
			processed_at TIMESTAMPTZ DEFAULT NOW()
		);`,
	}

	for i, q := range queries {
		log.Printf("📄 Выполняем миграцию %d:\n%s", i+1, q)
		if _, err := db.Exec(q); err != nil {
			log.Fatalf("❌ Ошибка миграции #%d: %v", i+1, err)
		}
	}
	log.Println("✅ Миграции выполнены.")
}

func Migrate(db *sql.DB) error {
	RunMigrations(db)
	return nil
}
