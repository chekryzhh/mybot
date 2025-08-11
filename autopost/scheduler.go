package autopost

import (
	"database/sql"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mybot/bot2"
)

func Start(bot *tgbotapi.BotAPI, db *sql.DB) {
	ticker := time.NewTicker(30 * time.Second)

	go func() {
		for {
			select {
			case <-ticker.C:
				bot2.PublishScheduledPosts(bot, db)
			}
		}
	}()
}
