package db

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"strings"
	"time"
)

func AddChannel(db *sql.DB, telegramChannelID int64, clientID int, title string, until time.Time) error {
	query := `
		INSERT INTO channels (telegram_channel_id, client_id, channel_title, subscription_until)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (telegram_channel_id) DO UPDATE
		SET channel_title = EXCLUDED.channel_title,
			subscription_until = EXCLUDED.subscription_until,
			is_active = TRUE;
	`
	_, err := db.Exec(query, telegramChannelID, clientID, title, until)
	if err != nil {
		log.Printf("❌ Ошибка при добавлении канала %d: %v", telegramChannelID, err)
	}
	return err
}

// Channel модель
type Channel struct {
	ID                int
	TelegramChannelID int64
	ClientID          int
	ChannelTitle      string
	WalletAddress     string
	IsActive          bool
	SubscriptionUntil time.Time
	CreatedAt         time.Time
	Username          string
}

// Получение канала по внутреннему ID
func GetChannelByID(db *sql.DB, id int) (Channel, error) {
	var c Channel
	var (
		title sql.NullString
		nt    sql.NullTime
		walt  sql.NullString
	)

	const query = `
		SELECT 
			id,
			telegram_channel_id,
			client_id,
			channel_title,
			subscription_until,
			is_active,
			wallet_address
		FROM channels
		WHERE id = $1
	`
	err := db.QueryRow(query, id).Scan(
		&c.ID,
		&c.TelegramChannelID,
		&c.ClientID,
		&title,
		&nt,
		&c.IsActive,
		&walt,
	)
	if err != nil {
		return c, err
	}

	if title.Valid {
		c.ChannelTitle = title.String
	}
	if nt.Valid {
		c.SubscriptionUntil = nt.Time
	}
	if walt.Valid {
		c.WalletAddress = walt.String
	}
	return c, nil
}

// Привязка канала по username
func SaveChannelForClient(bot *tgbotapi.BotAPI, db *sql.DB, clientChatID int64, username string) error {
	start := time.Now()

	// 1) Ищем клиента
	var clientID int
	if err := db.QueryRow(`SELECT id FROM clients WHERE chat_id = $1`, clientChatID).Scan(&clientID); err != nil {
		log.Printf("❌ SaveChannelForClient: клиент не найден chat_id=%d: %v", clientChatID, err)
		return err
	}
	log.Printf("🔎 SaveChannelForClient: client_id=%d для chat_id=%d", clientID, clientChatID)

	// 2) Нормализуем ввод (убираем пробелы и @)
	raw := username
	u := strings.TrimSpace(strings.TrimPrefix(username, "@"))
	log.Printf("🔧 Normalize username: raw=%q -> normalized=%q", raw, u)

	// 3) Получаем фактический чат и handle из Telegram
	chat, err := bot.GetChat(tgbotapi.ChatInfoConfig{
		ChatConfig: tgbotapi.ChatConfig{SuperGroupUsername: "@" + u},
	})
	if err != nil {
		log.Printf("❌ SaveChannelForClient: GetChat(@%s) ошибка: %v", u, err)
		return err
	}
	log.Printf("✅ SaveChannelForClient: TG chat OK: id=%d, username=%q, title=%q", chat.ID, chat.UserName, chat.Title)

	telegramChannelID := chat.ID
	finalHandle := u
	if chat.UserName != "" {
		finalHandle = chat.UserName // Telegram уже отдаёт без "@"
	}
	finalHandle = strings.TrimSpace(finalHandle)
	log.Printf("🔧 Final handle to store: %q", finalHandle)

	// 4) UPSERT: при конфликте обновляем title и активируем канал
	query := `
		INSERT INTO channels (telegram_channel_id, client_id, channel_title, is_active)
		VALUES ($1, $2, $3, TRUE)
		ON CONFLICT (client_id, telegram_channel_id) DO UPDATE
		SET channel_title = EXCLUDED.channel_title,
		    is_active = TRUE;
	`
	if _, err := db.Exec(query, telegramChannelID, clientID, finalHandle); err != nil {
		log.Printf("❌ SaveChannelForClient: UPSERT канал @%s (id=%d, client_id=%d) ошибка: %v",
			finalHandle, telegramChannelID, clientID, err)
		return err
	}
	log.Printf("✅ SaveChannelForClient: UPSERT @%s (id=%d) для client_id=%d выполнен",
		finalHandle, telegramChannelID, clientID)

	// 5) Пост‑проверка: та же строка в channels (должна существовать и быть связана с client_id)
	var chkID, chkClientID int
	var chkTitle string
	if err := db.QueryRow(`
		SELECT id, client_id, channel_title
		FROM channels
		WHERE telegram_channel_id = $1 AND client_id = $2
	`, telegramChannelID, clientID).Scan(&chkID, &chkClientID, &chkTitle); err != nil {
		log.Printf("⚠️ SaveChannelForClient: post-upsert SELECT не нашли строку (tg_id=%d, client_id=%d): %v",
			telegramChannelID, clientID, err)
	} else {
		log.Printf("🔎 SaveChannelForClient: post-upsert row -> id=%d, client_id=%d, title=%q",
			chkID, chkClientID, chkTitle)
	}

	// 6) Быстрый sanity-check: сколько каналов сейчас видит этот пользователь по JOIN
	var cnt int
	if err := db.QueryRow(`
		SELECT COUNT(*) 
		FROM channels c
		JOIN clients cl ON cl.id = c.client_id
		WHERE cl.chat_id = $1
	`, clientChatID).Scan(&cnt); err == nil {
		log.Printf("📊 SaveChannelForClient: теперь у chat_id=%d каналов в базе: %d", clientChatID, cnt)
	} else {
		log.Printf("⚠️ SaveChannelForClient: не удалось посчитать каналы пользователя: %v", err)
	}

	log.Printf("⏱ SaveChannelForClient: done in %s", time.Since(start))
	log.Printf("✅ Канал @%s (ID: %d) сохранён/обновлён для клиента %d",
		finalHandle, telegramChannelID, clientID)

	return nil
}

func GetChannelIDByUser(db *sql.DB, chatID int64) (int, error) {
	var id int
	err := db.QueryRow(`SELECT id FROM channels WHERE telegram_channel_id = $1`, chatID).Scan(&id)
	return id, err
}

func GetChannelsByUser(db *sql.DB, chatID int64) ([]Channel, error) {
	var channels []Channel

	rows, err := db.Query(`
		SELECT 
			c.id,
			c.telegram_channel_id,
			c.client_id,
			c.channel_title,
			c.subscription_until,
			c.is_active,
			c.wallet_address
		FROM channels c
		JOIN clients cl ON cl.id = c.client_id
		WHERE cl.chat_id = $1
		ORDER BY c.id DESC
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ch Channel
		var (
			title sql.NullString
			nt    sql.NullTime
			walt  sql.NullString
		)
		if err := rows.Scan(
			&ch.ID,
			&ch.TelegramChannelID,
			&ch.ClientID,
			&title,
			&nt,
			&ch.IsActive,
			&walt,
		); err != nil {
			return nil, err
		}
		if title.Valid {
			ch.ChannelTitle = title.String
		}
		if nt.Valid {
			ch.SubscriptionUntil = nt.Time
		}
		if walt.Valid {
			ch.WalletAddress = walt.String
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func GetChannelIDByUsername(conn *sql.DB, user string) (int, error) {
	user = strings.TrimSpace(user)
	user = strings.TrimPrefix(user, "@") // нормализуем

	var id int
	err := conn.QueryRow(`
		SELECT id
		FROM channels
		WHERE lower(channel_title) = lower($1)
		   OR lower(channel_title) = lower('@' || $1)
		LIMIT 1;
	`, user).Scan(&id)

	return id, err
}

// 🔧 ВАЖНО: Возвращает ИЛИ @username, ИЛИ -100id — но НИКОГДА @-100id
func GetChannelUsernameByID(db *sql.DB, id int) (string, error) {
	channel, err := GetChannelByID(db, id)
	if err != nil {
		return "", err
	}

	if channel.ChannelTitle != "" {
		return "@" + channel.ChannelTitle, nil
	}

	return fmt.Sprintf("%d", channel.TelegramChannelID), nil
}

func UpdateChannel(db *sql.DB, ch *Channel) error {
	_, err := db.Exec(`
		UPDATE channels
		SET subscription_until = $1, wallet_address = $2
		WHERE id = $3
	`, ch.SubscriptionUntil, ch.WalletAddress, ch.ID)
	return err
}

// Вернуть только активные каналы (subscription_until > now())
func GetActiveChannelsByUser(dbx *sql.DB, chatID int64) ([]Channel, error) {
	const q = `
SELECT c.id, c.telegram_channel_id, c.client_id, c.channel_title, c.wallet_address,
       c.is_active, c.subscription_until, c.created_at
FROM channels c
JOIN clients cl ON cl.id = c.client_id
WHERE cl.chat_id = $1
  AND c.subscription_until IS NOT NULL
  AND c.subscription_until > NOW()
ORDER BY c.created_at DESC`
	rows, err := dbx.Query(q, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var c Channel
		var title sql.NullString
		var walt sql.NullString
		var nt sql.NullTime
		if err := rows.Scan(
			&c.ID, &c.TelegramChannelID, &c.ClientID, &title, &walt,
			&c.IsActive, &nt, &c.CreatedAt,
		); err != nil {
			return nil, err
		}
		if title.Valid {
			c.ChannelTitle = title.String
		}
		if walt.Valid {
			c.WalletAddress = walt.String
		}
		if nt.Valid {
			c.SubscriptionUntil = nt.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChannelIDByUsernameAndChat возвращает id канала по @username для конкретного chat_id
func GetChannelIDByUsernameAndChat(dbConn *sql.DB, chatID int64, username string) (int, error) {
	u := strings.TrimPrefix(username, "@")
	var id int
	const q = `
		SELECT c.id
		FROM channels c
		JOIN clients cl ON cl.id = c.client_id
		WHERE cl.chat_id = $1 AND c.channel_title = $2
		LIMIT 1
	`
	if err := dbConn.QueryRow(q, chatID, u).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}
