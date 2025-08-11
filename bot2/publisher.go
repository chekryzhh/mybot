package bot2

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"mybot/api"
	"mybot/db"
	"mybot/pexels"
	"mybot/sub"
)

// Публикация всех запланированных постов, у которых время наступило
func PublishScheduledPosts(bot *tgbotapi.BotAPI, database *sql.DB) {
	now := time.Now()

	posts, err := db.GetScheduledPostsByTime(database, now)
	if err != nil {
		log.Println("❌ Ошибка при получении постов:", err)
		return
	}

	for _, post := range posts {
		// Берём канал (для paywall и служебных полей)
		ch, err := db.GetChannelByID(database, int(post.ChannelID))
		if err != nil {
			log.Printf("❌ Не удалось получить канал id=%d: %v\n", post.ChannelID, err)
			continue
		}

		// 🔒 Paywall: не публикуем без активной подписки (уведомление владельцу делает helper)
		if !sub.GuardActiveSubscription(bot, database, int(post.ChannelID), ch.ChannelTitle, ch.ClientID) {
			// подписка неактивна — пропускаем этот пост (оставляем в таблице)
			continue
		}

		// username канала для публикации (в формате "@channel")
		channelUsername, err := db.GetChannelUsernameByID(database, int(post.ChannelID))
		if err != nil || channelUsername == "" {
			log.Printf("❌ Не удалось получить username канала для channel_id=%d: %v\n", post.ChannelID, err)
			continue
		}

		// 1) Генерация текста поста
		style := map[string]string{
			"🤓 Экспертный":     "expert",
			"😊 Дружелюбный":    "friendly",
			"📢 Информационный": "informational",
			"🎭 Лирический":     "lyrical",
		}[post.Style]

		lang := map[string]string{
			"🇷🇺 Русский":    "ru",
			"🇬🇧 Английский": "en",
		}[post.Language]

		length := map[string]string{
			"✏️ Короткий": "short",
			"📄 Средний":   "medium",
			"📚 Длинный":   "long",
		}[post.Length]

		prompt := fmt.Sprintf(
			"Сгенерируй %s пост на тему %q в стиле %s на языке %s",
			length, post.Theme, style, lang,
		)

		text, _, err := api.GeneratePostFromPrompt(prompt)
		if err != nil || text == "" {
			log.Printf("❌ Ошибка генерации текста для channel_id=%d: %v", post.ChannelID, err)
			continue
		}

		// 2) Картинка
		if post.Photo != "" {
			// Фото, которое прислал пользователь
			photo := tgbotapi.NewPhotoToChannel(channelUsername, tgbotapi.FileID(post.Photo))
			if _, err := bot.Send(photo); err != nil {
				log.Printf("❌ Ошибка отправки фото в %s: %v", channelUsername, err)
				// не прерываем — текст всё равно отправим
			}
		} else {
			// Фото с Pexels по теме
			translated, err := api.Translate(post.Theme, "en")
			if err != nil || translated == "" {
				log.Printf("⚠️ Не удалось перевести тему %q, используем как есть", post.Theme)
				translated = post.Theme
			}

			imgURL, err := pexels.FetchImage(translated)
			if err != nil || imgURL == "" {
				log.Printf("⚠️ Не удалось найти фото по теме: %s (перевод: %s)", post.Theme, translated)
			} else {
				photo := tgbotapi.NewPhotoToChannel(channelUsername, tgbotapi.FileURL(imgURL))
				if _, err := bot.Send(photo); err != nil {
					log.Printf("❌ Ошибка отправки фото из Pexels в %s: %v", channelUsername, err)
				}
			}
		}

		// 3) Текст
		msg := tgbotapi.NewMessageToChannel(channelUsername, text)
		if _, err := bot.Send(msg); err != nil {
			log.Printf("❌ Ошибка публикации текста в %s: %v", channelUsername, err)
			continue
		}

		log.Printf("✅ Пост опубликован в %s", channelUsername)

		// 4) Удаляем задачу из расписания
		if err := db.DeleteScheduledPostByID(database, post.ID); err != nil {
			log.Printf("❌ Не удалось удалить запланированный пост #%d: %v", post.ID, err)
		}
	}
}

// Для предпросмотра в UI/логах
func RegenerateContent(post *db.ScheduledPost) string {
	return fmt.Sprintf("📝 Тема: %s\n✍️ Стиль: %s\n🌐 Язык: %s\n📄 Длина: %s",
		post.Theme, post.Style, post.Language, post.Length)
}
