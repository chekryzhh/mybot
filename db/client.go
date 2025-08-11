package db

import (
	"database/sql"
)

type Client struct {
	ID       int64
	ChatID   int64
	Username string
	SQL      *sql.DB
}

func GetOrCreateClient(db *sql.DB, chatID int64, username string) (*Client, error) {
	var c Client
	err := db.QueryRow(`
		INSERT INTO clients (chat_id, username)
		VALUES ($1, $2)
		ON CONFLICT (chat_id) DO UPDATE SET username = EXCLUDED.username
		RETURNING id, chat_id, username
	`, chatID, username).Scan(&c.ID, &c.ChatID, &c.Username)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func CreateClient(db *sql.DB, chatID int64, username string) error {
	_, err := db.Exec(`
		INSERT INTO clients (chat_id, username)
		VALUES ($1, $2)
		ON CONFLICT (chat_id) DO NOTHING
	`, chatID, username)
	return err
}
func GetClientByID(db *sql.DB, id int) (*Client, error) {
	var c Client
	err := db.QueryRow(`
		SELECT id, chat_id, username
		FROM clients
		WHERE id = $1
	`, id).Scan(&c.ID, &c.ChatID, &c.Username)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
