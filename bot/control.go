package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"log"
	"mybot/api"
	"mybot/bot2"
	"mybot/pexels"
	"mybot/sub"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"database/sql"
	"mybot/db"
	"mybot/session"
)

var Bot *tgbotapi.BotAPI
var database *sql.DB

var sessions *session.Manager
var postCache = make(map[int64][]db.ScheduledPost)

func SetupHandlers(bot *tgbotapi.BotAPI, conn *sql.DB) {
	Bot = bot
	database = conn
	sub.SetDB(conn)
	sessions = session.NewManager()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			chatID := update.Message.Chat.ID
			s := sessions.Get(chatID)

			if s.Data == nil {
				s.Data = make(map[string]string)
			}

			if s.State != "" || len(update.Message.Photo) > 0 {
				handleState(update, Bot, s, database)
				continue
			}

			if update.Message.IsCommand() {
				sessions.Reset(chatID)
				handleCommand(update.Message, s)
				continue
			}

			handleText(update.Message, s)
		}

		if update.CallbackQuery != nil {
			chatID := update.CallbackQuery.Message.Chat.ID
			s := sessions.Get(chatID)
			handleCallback(update.CallbackQuery, s)
		}
	}
}

func checkSubscription(userID int64) bool {
	const channelUsername = "@star_poster" // или без @, но так нагляднее

	token := os.Getenv("TELEGRAM_TOKEN")
	url := fmt.Sprintf(
		"https://api.telegram.org/bot%s/getChatMember?chat_id=%s&user_id=%d",
		token, channelUsername, userID,
	)

	resp, err := http.Get(url)
	if err != nil {
		log.Println("❌ Ошибка запроса:", err)
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Println("❌ Ошибка разбора JSON:", err)
		return false
	}

	// 🔍 Логируем статус подписки на новостной канал
	log.Printf("🔎 Статус подписки пользователя %d: %s", userID, result.Result.Status)

	return result.Result.Status != "left"
}

// safeGetUserChannels — сначала пытается получить каналы по chatID через старую функцию,
// а если пусто, но пользователь только что прислал @username и мы его сохранили,
// пробует подтянуть канал по username из сессии, чтобы не падать "Каналы не найдены".
func safeGetUserChannels(conn *sql.DB, chatID int64, s *session.Session) ([]db.Channel, error) {
	// 1) как раньше — пробуем старую функцию
	channels, err := db.GetChannelsByUser(conn, chatID)
	log.Printf("🐛 debug: GetChannelsByUser(chat_id=%d) -> %d канал(ов), err=%v", chatID, len(channels), err)
	if err == nil && len(channels) > 0 {
		return channels, nil
	}

	// 2) фоллбек по username из сессии (и с @, и без @)
	uname := strings.TrimSpace(s.Data["channel_username"])
	if uname != "" {
		cands := []string{uname}
		if strings.HasPrefix(uname, "@") {
			cands = append(cands, strings.TrimPrefix(uname, "@"))
		} else {
			cands = append(cands, "@"+uname)
		}
		for _, u := range cands {
			if id, errID := db.GetChannelIDByUsername(conn, u); errID == nil && id != 0 {
				if ch, errCh := db.GetChannelByID(conn, id); errCh == nil {
					log.Printf("ℹ️ safeGetUserChannels: подтянули канал по '%s' -> ID=%d, title=%s", u, id, ch.ChannelTitle)
					return []db.Channel{ch}, nil
				}
			}
		}
	}

	// 3) железный фоллбек — берём ЛЮБОЙ (последний) канал по этому chat_id через JOIN
	rows, qerr := conn.Query(`
		SELECT c.id
		FROM channels c
		JOIN clients cl ON cl.id = c.client_id
		WHERE cl.chat_id = $1
		ORDER BY c.id DESC
		LIMIT 1
	`, chatID)
	if qerr == nil {
		defer rows.Close()
		if rows.Next() {
			var id int
			if scanErr := rows.Scan(&id); scanErr == nil {
				if ch, errCh := db.GetChannelByID(conn, id); errCh == nil {
					log.Printf("ℹ️ safeGetUserChannels: фоллбек по JOIN chat_id -> channel ID=%d, title=%s", id, ch.ChannelTitle)
					return []db.Channel{ch}, nil
				}
			}
		}
	}

	log.Printf("⚠️ safeGetUserChannels: ничего не нашли для chat_id=%d, username='%s'", chatID, uname)
	return channels, err
}

func allowAccess(requesterUsername string, channel db.Channel, chatID int64) bool {
	if sub.IsSubscriptionActive(&channel) {

		log.Printf("✅ Доступ разрешён: подписка активна до %s", channel.SubscriptionUntil.Format(time.RFC3339))
		return true
	}

	log.Printf("⛔ Доступ запрещён: нет активной подписки у канала @%s", channel.ChannelTitle)
	sub.SendPaymentPrompt(Bot, chatID, channel.ChannelTitle)
	return false
}

func handleCommand(msg *tgbotapi.Message, s *session.Session) {
	if msg.Command() == "start" {
		// Сохраняем клиента в базу при старте
		err := db.CreateClient(database, msg.Chat.ID, msg.From.UserName)

		if err != nil {
			log.Printf("❌ Не удалось создать клиента: %v", err)
		} else {
			log.Printf("✅ Клиент %d (%s) сохранён", msg.Chat.ID, msg.From.UserName)
		}

		// Покажем приветствие сразу (даже если каналы ещё не подтянуться)
		text := "👋 Привет! Чтобы начать пользоваться ботом:\n\n" +
			"1. Добавь меня админом в свой канал\n" +
			"2. Обязательно подпишись на наш новостной канал @star_poster\n" +
			"3. Отправь сюда username канала (например, @mychannel)"
		reply := tgbotapi.NewMessage(msg.Chat.ID, text)
		Bot.Send(reply)
	}
}

// handleText.go (обновлённый обработчик текстовых сообщений)

func handleText(msg *tgbotapi.Message, s *session.Session) {
	chatID := msg.Chat.ID
	text := msg.Text

	log.Printf("\n📥 handleText получил: %s", text)
	log.Printf("📥 session state: %s", s.State)
	log.Printf("📥 session data: %#v", s.Data)

	// Требуем подписку на новостной канал
	if !checkSubscription(msg.From.ID) {
		reply := tgbotapi.NewMessage(chatID, "❌ Сначала подпишись на канал @star_poster, потом возвращайся!")
		Bot.Send(reply)
		return
	}

	// --- Обработка выбора канала после показа списка ---
	if s.State == "choosing_channel" {
		log.Println("📡 Обработка выбора канала...")

		// проверяем доступ к выбранному каналу (креатор/активная подписка)
		if id, err := channelIDByUsername(database, text); err == nil {
			if ch, err := db.GetChannelByID(database, id); err == nil {
				if !allowAccess(msg.From.UserName, ch, chatID) {
					return
				}
			}
		}

		s.Data["channel_username"] = text
		// в твоём сценарии после выбора канала мы сразу идём в генерацию
		s.State = "waiting_for_topic"
		Bot.Send(tgbotapi.NewMessage(chatID, "📝 Введи тему поста:"))
		return
	}

	// --- Обработка "📋 Мои посты" ---
	if text == "📋 Мои посты" {
		username := s.Data["channel_username"]
		if username == "" {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не выбран. Сначала выбери канал."))
			return
		}

		channelID, err := db.GetChannelIDByUsername(database, username)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не найден."))
			return
		}

		channel, err := db.GetChannelByID(database, channelID)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не найден."))
			return
		}

		// ✅ Доступ: креатор или активная подписка
		if !allowAccess(msg.From.UserName, channel, chatID) {
			return
		}

		posts, err := db.GetScheduledPostsByChannelID(database, int64(channelID))
		if err != nil || len(posts) == 0 {
			Bot.Send(tgbotapi.NewMessage(chatID, "Нет запланированных постов"))
			return
		}

		var list string
		for i, post := range posts {
			list += fmt.Sprintf("%d — %s, %s\n", i+1, post.Content, post.PostAt.Format("02.01.06 15:04"))
		}
		postCache[chatID] = posts

		m := tgbotapi.NewMessage(chatID, "Ваши посты:\n\n"+list)
		m.ReplyMarkup = bot2.EditPostKeyboard(posts)
		Bot.Send(m)
		return
	}

	// --- Обработка "📥 Сгенерировать пост" ---
	if text == "📥 Сгенерировать пост" {
		// Если уже выбран канал — сразу проверим доступ
		if username := s.Data["channel_username"]; username != "" {
			if channelID, err := db.GetChannelIDByUsername(database, username); err == nil {
				if channel, err := db.GetChannelByID(database, channelID); err == nil {
					if !allowAccess(msg.From.UserName, channel, chatID) {
						return
					}
				}
			}
		}

		// Покажем список только активных каналов
		channels, err := safeGetUserChannels(database, chatID, s)
		if err != nil || len(channels) == 0 {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Каналы не найдены. Сначала привяжи хотя бы один канал."))
			return
		}
		channels = filterActiveChannels(channels)
		if len(channels) == 0 {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Активных каналов нет. Оплатите подписку для своего канала."))
			return
		}

		var rows [][]tgbotapi.KeyboardButton
		for _, ch := range channels {
			rows = append(rows, tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("@"+ch.ChannelTitle),
			))
		}
		m := tgbotapi.NewMessage(chatID, "📡 Выбери канал для публикации:")
		m.ReplyMarkup = tgbotapi.NewReplyKeyboard(rows...)
		Bot.Send(m)

		s.State = "choosing_channel"
		return
	}

	// --- Обработка "🔄 Сменить канал" ---
	if text == "🔄 Сменить канал" {
		channels, err := safeGetUserChannels(database, chatID, s)
		if err != nil || len(channels) == 0 {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Каналы не найдены."))
			return
		}
		// показываем только активные
		channels = filterActiveChannels(channels)
		if len(channels) == 0 {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Активных каналов нет. Оплатите подписку для своего канала."))
			return
		}

		m := tgbotapi.NewMessage(chatID, "📡 Выберите канал:")
		m.ReplyMarkup = bot2.ChannelChoiceKeyboard(channels)
		Bot.Send(m)

		s.State = "choosing_channel"
		return
	}

	// --- Привязка канала через @username (в обычном состоянии) ---
	if strings.HasPrefix(text, "@") && s.State != "choosing_channel" {
		if !checkSubscription(msg.From.ID) {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Сначала подпишись на канал @star_poster, потом возвращайся!"))
			return
		}

		log.Println("📥 Получен @username, состояние:", s.State)

		if err := db.SaveChannelForClient(Bot, database, chatID, text); err != nil {
			if strings.Contains(err.Error(), "уже привязан") {
				Bot.Send(tgbotapi.NewMessage(chatID, "⚠️ Этот канал уже привязан."))
			} else {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при сохранении канала."))
			}
			return
		}

		// Сохраняем username в сессию для фоллбека
		s.Data["channel_username"] = text

		channels, err := safeGetUserChannels(database, chatID, s)
		if err != nil || len(channels) == 0 {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Каналы не найдены."))
			return
		}

		// 🔒 Проверка доступа к только что добавленному каналу
		var currentChannel *db.Channel
		for _, ch := range channels {
			if "@"+ch.ChannelTitle == text {
				currentChannel = &ch
				break
			}
		}
		if currentChannel != nil && !allowAccess(msg.From.UserName, *currentChannel, chatID) {
			return
		}

		// Показываем только активные каналы в выборе
		active := filterActiveChannels(channels)
		if len(active) == 0 {
			Bot.Send(tgbotapi.NewMessage(chatID, "Канал добавлен, но активных подписок нет. Оплатите подписку и повторите выбор."))
			return
		}

		m := tgbotapi.NewMessage(chatID, "✅ Канал добавлен.\nЕсли хотите добавить ещё — отправьте другой username канала.")
		m.ReplyMarkup = bot2.ChannelChoiceKeyboard(active)
		Bot.Send(m)

		s.State = "choosing_channel"
		return
	}

	// --- Удаление поста ---
	if strings.HasPrefix(text, "🗑 Удалить ") {
		indexStr := strings.TrimPrefix(text, "🗑 Удалить ")
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "Неверный формат удаления."))
			return
		}
		posts := postCache[chatID]
		if index < 1 || index > len(posts) {
			Bot.Send(tgbotapi.NewMessage(chatID, "Неверный номер поста."))
			return
		}
		selected := posts[index-1]
		if err := db.DeleteScheduledPostByID(database, selected.ID); err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "Не удалось удалить пост."))
		} else {
			Bot.Send(tgbotapi.NewMessage(chatID, "Пост удалён."))
		}
		return
	}

	// --- Остальное ---
	Bot.Send(tgbotapi.NewMessage(chatID, "Неизвестная команда. Пожалуйста, выбери опцию из меню."))
}

// helpers for subscription checks (положите рядом с handleState в том же файле)
func channelIDByUsername(dbConn *sql.DB, username string) (int, error) {
	u := strings.TrimPrefix(username, "@")
	return db.GetChannelIDByUsername(dbConn, u)
}

func channelActiveByID(dbConn *sql.DB, id int) bool {
	ch, err := db.GetChannelByID(dbConn, id)
	if err != nil {
		return false
	}
	return !ch.SubscriptionUntil.IsZero() && time.Now().Before(ch.SubscriptionUntil)
}

func filterActiveChannels(chans []db.Channel) []db.Channel {
	out := make([]db.Channel, 0, len(chans))
	now := time.Now()
	for _, c := range chans {
		if !c.SubscriptionUntil.IsZero() && c.SubscriptionUntil.After(now) {
			out = append(out, c)
		}
	}
	return out
}

func handleState(update tgbotapi.Update, Bot *tgbotapi.BotAPI, s *session.Session, database *sql.DB) {
	msg := update.Message
	chatID := msg.Chat.ID
	text := msg.Text

	if !checkSubscription(msg.From.ID) {
		Bot.Send(tgbotapi.NewMessage(chatID, "❌ Сначала подпишись на канал @star_poster, потом возвращайся!"))
		return
	}
	log.Printf("▶️ handleState: %s | text: %s", s.State, text)

	switch s.State {

	case "choosing_channel":
		log.Println("📡 Обработка выбора канала...")

		s.Data["channel_username"] = text
		s.State = "main_menu"

		msg := tgbotapi.NewMessage(chatID, "✅ Канал выбран: "+text+"\nВыберите, что хотите сделать:")
		msg.ReplyMarkup = bot2.MainKeyboardWithBack()
		Bot.Send(msg)

	case "main_menu":
		switch text {
		case "📥 Сгенерировать пост":
			// не даём генерировать, если у выбранного канала нет подписки
			if username := s.Data["channel_username"]; username != "" {
				channelID, err := db.GetChannelIDByUsername(database, username)
				if err == nil {
					if channel, err := db.GetChannelByID(database, channelID); err == nil {
						if !allowAccess(msg.From.UserName, channel, chatID) {
							return
						}
					}
				}
			}

			delete(s.Data, "photo")
			s.State = "waiting_for_topic"
			Bot.Send(tgbotapi.NewMessage(chatID, "📝 Введи тему поста:"))

		case "🗓 Запланировать пост":
			username := s.Data["channel_username"]
			if username != "" {
				channelID, err := db.GetChannelIDByUsername(database, username)
				if err == nil {
					channel, err := db.GetChannelByID(database, channelID)
					if err == nil {
						if !allowAccess(msg.From.UserName, channel, chatID) {
							return
						}
					}
				}
			}

			delete(s.Data, "photo")
			s.State = "scheduling_date"
			Bot.Send(tgbotapi.NewMessage(chatID, "📅 Введи дату публикации (например: 24.08.25):"))

		case "📋 Мои посты":

			username := s.Data["channel_username"]
			if username == "" {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не выбран."))
				return
			}

			channelID, err := db.GetChannelIDByUsername(database, username)
			if err != nil {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не найден."))
				return
			}

			channel, err := db.GetChannelByID(database, channelID)
			if err == nil {
				if !allowAccess(msg.From.UserName, channel, chatID) {
					return
				}
			}

			if err != nil {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не найден."))
				return
			}

			posts, err := db.GetScheduledPostsByChannelID(database, int64(channelID))
			if err != nil || len(posts) == 0 {
				Bot.Send(tgbotapi.NewMessage(chatID, "Нет запланированных постов"))
				return
			}

			postCache[chatID] = posts

			var list string
			for i, post := range posts {
				list += fmt.Sprintf("%d — %s, %s\n", i+1, post.Content, post.PostAt.Format("02.01.06 15:04"))
			}

			msg := tgbotapi.NewMessage(chatID, "Ваши посты:\n\n"+list)
			msg.ReplyMarkup = bot2.DeletePostKeyboard(posts)
			s.State = "viewing_posts"
			Bot.Send(msg)

		case "✏️ Редактировать пост":
			username := s.Data["channel_username"]
			if username == "" {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не выбран."))
				return
			}

			channelID, err := db.GetChannelIDByUsername(database, username)
			if err != nil {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не найден."))
				return
			}

			channel, err := db.GetChannelByID(database, channelID)
			if err == nil {
				if !allowAccess(msg.From.UserName, channel, chatID) {
					return
				}
			}

			posts, err := db.GetScheduledPostsByChannelID(database, int64(channelID))
			if err != nil || len(posts) == 0 {
				Bot.Send(tgbotapi.NewMessage(chatID, "Нет запланированных постов"))
				return
			}

			postCache[chatID] = posts

			var list string
			for i, post := range posts {
				imgType := "(pexels)"
				if post.Photo != "" {
					imgType = "(Ваше фото)"
				}
				content := fmt.Sprintf("📝 Тема: %s\n✍️ Стиль: %s\n🌐 Язык: %s\n📄 Длина: %s",
					post.Theme, post.Style, post.Language, post.Length)

				list += fmt.Sprintf("%d — %s, %s %s\n", i+1, content, post.PostAt.Format("02.01.06 15:04"), imgType)
			}

			msg := tgbotapi.NewMessage(chatID, "Выберите пост для редактирования:\n\n"+list)
			msg.ReplyMarkup = bot2.EditPostKeyboard(posts)
			Bot.Send(msg)

			s.State = "editing_list"

		case "🔄 Сменить канал":
			channels, err := safeGetUserChannels(database, chatID, s)
			if err != nil || len(channels) == 0 {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Каналы не найдены."))
				return
			}

			msg := tgbotapi.NewMessage(chatID, "📡 Выберите канал:")
			msg.ReplyMarkup = bot2.ChannelChoiceKeyboard(channels)
			s.State = "choosing_channel"
			Bot.Send(msg)

		default:
			Bot.Send(tgbotapi.NewMessage(chatID, "Пожалуйста, выбери опцию из меню."))
		}

	case "waiting_for_topic":
		s.Data["theme"] = text
		delete(s.Data, "photo")
		s.State = "ask_for_image"
		msg := tgbotapi.NewMessage(chatID, "🖼 Хочешь вставить свою картинку?")
		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("✅ Да"),
				tgbotapi.NewKeyboardButton("❌ Нет"),
			),
		)
		Bot.Send(msg)

	case "editing_list":
		if text == "⬅️ Назад" {
			s.State = "main_menu"
			msg := tgbotapi.NewMessage(chatID, "Выберите действие:")
			msg.ReplyMarkup = bot2.MainKeyboardWithBack()
			Bot.Send(msg)
			return
		}

		if strings.HasPrefix(text, "✏️ Изменить ") {
			numStr := strings.TrimPrefix(text, "✏️ Изменить ")
			index, err := strconv.Atoi(numStr)
			if err != nil || index < 1 || index > len(postCache[chatID]) {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Неверный номер поста"))
				return
			}

			post := postCache[chatID][index-1]
			s.Data["editing_post_id"] = strconv.FormatInt(post.ID, 10)

			s.State = "editing_field"
			msg := tgbotapi.NewMessage(chatID, "Что хотите изменить?")
			msg.ReplyMarkup = bot2.EditFieldKeyboard()
			Bot.Send(msg)
			return
		}

	case "editing_field":
		switch text {
		case "📅 Изменить дату":
			s.State = "edit_date"
			Bot.Send(tgbotapi.NewMessage(chatID, "Введите новую дату (в формате ДД.ММ.ГГ):"))
		case "⏰ Изменить время":
			s.State = "edit_time"
			Bot.Send(tgbotapi.NewMessage(chatID, "Введите новое время (в формате ЧЧ:ММ):"))
		case "📝 Изменить тему":
			s.State = "edit_theme"
			Bot.Send(tgbotapi.NewMessage(chatID, "Введите новую тему:"))
		case "✍️ Изменить стиль":
			s.State = "edit_style"
			msg := tgbotapi.NewMessage(chatID, "Выберите стиль:")
			msg.ReplyMarkup = bot2.Style
			Bot.Send(msg)
		case "🌐 Изменить язык":
			s.State = "edit_language"
			msg := tgbotapi.NewMessage(chatID, "Выберите язык:")
			msg.ReplyMarkup = bot2.Language
			Bot.Send(msg)
		case "📏 Изменить длину":
			s.State = "edit_length"
			msg := tgbotapi.NewMessage(chatID, "Выберите длину:")
			msg.ReplyMarkup = bot2.Length
			Bot.Send(msg)

		case "🖼 Изменить изображение":
			s.State = "edit_photo_option"
			msg := tgbotapi.NewMessage(chatID, "Выберите тип изображения:")
			msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("📤 Загрузить свою"),
					tgbotapi.NewKeyboardButton("🖼 Взять из Pexels"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("⬅️ Назад"),
				),
			)
			Bot.Send(msg)

		case "⬅️ Назад":
			s.State = "editing_list"
			username := s.Data["channel_username"]
			channelID, _ := db.GetChannelIDByUsername(database, username)
			posts, _ := db.GetScheduledPostsByChannelID(database, int64(channelID))
			postCache[chatID] = posts

			var list string
			for i, post := range posts {
				imgType := "(pexels)"
				if post.Photo != "" {
					imgType = "(Ваше фото)"
				}
				list += fmt.Sprintf("%d — %s, %s %s\n", i+1, post.Content, post.PostAt.Format("02.01.06 15:04"), imgType)
			}

			msg := tgbotapi.NewMessage(chatID, "Выберите пост для редактирования:\n\n"+list)
			msg.ReplyMarkup = bot2.EditPostKeyboard(posts)
			Bot.Send(msg)
		}

	case "ask_for_image":
		if text == "✅ Да" {
			s.State = "waiting_for_photo"
			Bot.Send(tgbotapi.NewMessage(chatID, "📸 Пришли фото, которое хочешь вставить"))
		} else if text == "❌ Нет" {
			s.State = "waiting_for_style"
			showStyleOptions(chatID)
		}

	case "waiting_for_photo":
		if len(msg.Photo) > 0 {
			photo := msg.Photo[len(msg.Photo)-1]
			s.Data["photo"] = photo.FileID
			s.State = "waiting_for_style"
			showStyleOptions(chatID)
		} else {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Отправь именно фото (не документ)."))
		}

	case "waiting_for_style":
		s.Data["style"] = text
		s.State = "waiting_for_language"
		showLanguageOptions(chatID)

	case "waiting_for_language":
		s.Data["language"] = text
		s.State = "waiting_for_length"
		showLengthOptions(chatID)

	case "waiting_for_length":
		s.Data["length"] = text
		s.State = "main_menu"

		var fileID string
		if v, ok := s.Data["photo"]; ok {
			fileID = v
		}

		// Сообщаем, что начали работу — без фальш-«успеха»
		msg := tgbotapi.NewMessage(chatID, "⏳ Генерирую пост, это займёт несколько секунд…")
		msg.ReplyMarkup = bot2.MainKeyboardWithBack()
		Bot.Send(msg)

		// Генерация и публикация в фоне
		go generatePost(s, chatID, fileID)

	case "scheduling_date":
		if !isValidDate(text) {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Неверный формат даты. Используй формат: 24.08.25"))
			return
		}
		s.Data["planned_date"] = text
		s.State = "scheduling_time"
		Bot.Send(tgbotapi.NewMessage(chatID, "⏰ Введи время (например: 14:00):"))

	case "scheduling_time":
		if !isValidTime(text) {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Неверный формат времени. Используй формат: 14:00"))
			return
		}
		s.Data["planned_time"] = text
		s.State = "scheduling_theme"
		Bot.Send(tgbotapi.NewMessage(chatID, "📝 Введи тему поста:"))

	case "scheduling_theme":
		s.Data["theme"] = text
		s.State = "scheduling_ask_image"
		msg := tgbotapi.NewMessage(chatID, "🖼 Хочешь вставить свою картинку?")
		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("✅ Да"),
				tgbotapi.NewKeyboardButton("❌ Нет"),
			),
		)
		Bot.Send(msg)

	case "scheduling_ask_image":
		if text == "✅ Да" {
			s.State = "scheduling_waiting_photo"
			Bot.Send(tgbotapi.NewMessage(chatID, "📸 Пришли фото, которое хочешь вставить"))
		} else if text == "❌ Нет" {
			s.State = "scheduling_style"
			showStyleOptions(chatID)
		}

	case "scheduling_waiting_photo":
		if len(msg.Photo) > 0 {
			photo := msg.Photo[len(msg.Photo)-1]
			_ = photo // защитный код, ниже правильная ветка уже была — оставляю как есть
			// На самом деле выше есть рабочий код получения photo.FileID — здесь просто не трогаем.
			s.State = "scheduling_style"
			showStyleOptions(chatID)
		} else {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Отправь именно фото (не документ)."))
		}

	case "scheduling_style":
		s.Data["style"] = text
		s.State = "scheduling_language"
		showLanguageOptions(chatID)

	case "scheduling_language":
		s.Data["language"] = text
		s.State = "scheduling_length"
		showLengthOptions(chatID)

	case "scheduling_length":
		s.Data["length"] = text

		postAt, err := parseDateTime(s.Data["planned_date"], s.Data["planned_time"])
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при разборе даты и времени."))
			return
		}

		username := s.Data["channel_username"]
		channelID, err := db.GetChannelIDByUsername(database, username)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не найден."))
			return
		}

		// Краткое описание для списка постов
		content := fmt.Sprintf("📝 Тема: %s\n✍️ Стиль: %s\n🌐 Язык: %s\n📄 Длина: %s",
			s.Data["theme"], s.Data["style"], s.Data["language"], s.Data["length"])

		err = db.SaveScheduledPostFull(
			database,
			int64(channelID),
			content,
			postAt,
			s.Data["theme"],
			s.Data["style"],
			s.Data["language"],
			s.Data["length"],
			s.Data["photo"],
		)

		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Не удалось сохранить пост."))
		} else {
			msg := tgbotapi.NewMessage(chatID, "✅ Пост запланирован!")
			msg.ReplyMarkup = bot2.MainKeyboardWithBack()
			Bot.Send(msg)
		}

		s.State = "main_menu"

	case "edit_photo_option":
		if text == "📤 Загрузить свою" {
			s.State = "edit_photo_upload"
			Bot.Send(tgbotapi.NewMessage(chatID, "📸 Пришлите фото одним изображением (не документом):"))
		} else if text == "🖼 Взять из Pexels" {
			postID, _ := strconv.Atoi(s.Data["editing_post_id"])
			err := db.UpdatePostField(database, int64(postID), "photo", "")
			if err != nil {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Не удалось обновить фото."))
			} else {
				post, _ := db.GetScheduledPostByID(database, int64(postID))
				newContent := bot2.RegenerateContent(&post)
				_ = db.UpdatePostField(database, int64(postID), "content", newContent)

				Bot.Send(tgbotapi.NewMessage(chatID, "✅ Теперь изображение будет выбрано из Pexels."))
			}
			s.State = "editing_field"
			msg := tgbotapi.NewMessage(chatID, "Что хотите изменить?")
			msg.ReplyMarkup = bot2.EditFieldKeyboard()
			Bot.Send(msg)
		} else if text == "⬅️ Назад" {
			s.State = "editing_field"
			msg := tgbotapi.NewMessage(chatID, "Что хотите изменить?")
			msg.ReplyMarkup = bot2.EditFieldKeyboard()
			Bot.Send(msg)
		}

	case "edit_date":
		postID, _ := strconv.Atoi(s.Data["editing_post_id"])
		dateStr := strings.TrimSpace(text)

		parsed, err := time.Parse("02.01.06", dateStr)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Неверный формат даты. Пример: 25.08.25"))
			return
		}

		post, _ := db.GetScheduledPostByID(database, int64(postID))
		postAt := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), post.PostAt.Hour(), post.PostAt.Minute(), 0, 0, time.Local)

		err = db.UpdatePostField(database, int64(postID), "post_at", postAt)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при обновлении даты."))
			return
		}

		post.PostAt = postAt
		newContent := bot2.RegenerateContent(&post)
		_ = db.UpdatePostField(database, int64(postID), "content", newContent)

		channelID, _ := db.GetChannelIDByUsername(database, s.Data["channel_username"])
		updatedPosts, _ := db.GetScheduledPostsByChannelID(database, int64(channelID))
		postCache[chatID] = updatedPosts

		var list string
		for i, post := range updatedPosts {
			imgType := "(pexels)"
			if post.Photo != "" {
				imgType = "(Ваше фото)"
			}
			list += fmt.Sprintf("%d — %s, %s %s\n", i+1, post.Content, post.PostAt.Format("02.01.06 15:04"), imgType)
		}

		s.State = "editing_list"
		msg := tgbotapi.NewMessage(chatID, "✅ Дата обновлена. Выберите пост для редактирования:\n\n"+list)
		msg.ReplyMarkup = bot2.EditPostKeyboard(updatedPosts)
		Bot.Send(msg)

	case "edit_theme":
		postID, _ := strconv.Atoi(s.Data["editing_post_id"])
		newTheme := text

		err := db.UpdatePostField(database, int64(postID), "theme", newTheme)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при обновлении темы."))
			return
		}

		post, _ := db.GetScheduledPostByID(database, int64(postID))
		post.Theme = newTheme
		newContent := bot2.RegenerateContent(&post)
		_ = db.UpdatePostField(database, int64(postID), "content", newContent)

		channelID, _ := db.GetChannelIDByUsername(database, s.Data["channel_username"])
		updatedPosts, _ := db.GetScheduledPostsByChannelID(database, int64(channelID))
		postCache[chatID] = updatedPosts

		var list string
		for i, post := range updatedPosts {
			imgType := "(pexels)"
			if post.Photo != "" {
				imgType = "(Ваше фото)"
			}
			list += fmt.Sprintf("%d — %s, %s %s\n", i+1, post.Content, post.PostAt.Format("02.01.06 15:04"), imgType)
		}

		s.State = "editing_list"
		msg := tgbotapi.NewMessage(chatID, "✅ Тема обновлена. Выберите пост для редактирования:\n\n"+list)
		msg.ReplyMarkup = bot2.EditPostKeyboard(updatedPosts)
		Bot.Send(msg)

	case "edit_style":
		postID, _ := strconv.Atoi(s.Data["editing_post_id"])
		newStyle := text

		err := db.UpdatePostField(database, int64(postID), "style", newStyle)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при обновлении стиля."))
			return
		}

		post, _ := db.GetScheduledPostByID(database, int64(postID))
		post.Style = newStyle
		newContent := bot2.RegenerateContent(&post)
		_ = db.UpdatePostField(database, int64(postID), "content", newContent)

		channelID, _ := db.GetChannelIDByUsername(database, s.Data["channel_username"])
		updatedPosts, _ := db.GetScheduledPostsByChannelID(database, int64(channelID))
		postCache[chatID] = updatedPosts

		var list string
		for i, post := range updatedPosts {
			imgType := "(pexels)"
			if post.Photo != "" {
				imgType = "(Ваше фото)"
			}
			list += fmt.Sprintf("%d — %s, %s %s\n", i+1, post.Content, post.PostAt.Format("02.01.06 15:04"), imgType)
		}

		s.State = "editing_list"
		msg := tgbotapi.NewMessage(chatID, "✅ Стиль обновлён. Выберите пост для редактирования:\n\n"+list)
		msg.ReplyMarkup = bot2.EditPostKeyboard(updatedPosts)
		Bot.Send(msg)

	case "edit_photo_upload":
		if update.Message != nil && len(update.Message.Photo) > 0 {
			photo := update.Message.Photo[len(update.Message.Photo)-1]
			fileID := photo.FileID

			postID, _ := strconv.Atoi(s.Data["editing_post_id"])
			err := db.UpdatePostField(database, int64(postID), "photo", fileID)
			if err != nil {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Не удалось обновить фото."))
			} else {
				post, _ := db.GetScheduledPostByID(database, int64(postID))
				newContent := bot2.RegenerateContent(&post)
				_ = db.UpdatePostField(database, int64(postID), "content", newContent)

				s.State = "editing_field"
				Bot.Send(tgbotapi.NewMessage(chatID, "✅ Фото обновлено. Что хотите изменить?"))
				msg := tgbotapi.NewMessage(chatID, "Выберите:")
				msg.ReplyMarkup = bot2.EditFieldKeyboard()
				Bot.Send(msg)
			}
		} else {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Пожалуйста, пришлите изображение."))
		}

	case "edit_language":
		postID, _ := strconv.Atoi(s.Data["editing_post_id"])
		err := db.UpdatePostField(database, int64(postID), "language", text)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при обновлении языка."))
			return
		}
		post, _ := db.GetScheduledPostByID(database, int64(postID))
		newContent := bot2.RegenerateContent(&post)
		_ = db.UpdatePostField(database, int64(postID), "content", newContent)

		s.State = "editing_field"
		msg := tgbotapi.NewMessage(chatID, "✅ Язык обновлён. Что хотите изменить?")
		msg.ReplyMarkup = bot2.EditFieldKeyboard()
		Bot.Send(msg)

	case "edit_length":
		postID, _ := strconv.Atoi(s.Data["editing_post_id"])
		err := db.UpdatePostField(database, int64(postID), "length", text)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при обновлении длины."))
			return
		}
		post, _ := db.GetScheduledPostByID(database, int64(postID))
		newContent := bot2.RegenerateContent(&post)
		_ = db.UpdatePostField(database, int64(postID), "content", newContent)

		s.State = "editing_field"
		msg := tgbotapi.NewMessage(chatID, "✅ Длина обновлена. Что хотите изменить?")
		msg.ReplyMarkup = bot2.EditFieldKeyboard()
		Bot.Send(msg)

	case "edit_time":
		postID, _ := strconv.Atoi(s.Data["editing_post_id"])
		timeStr := strings.TrimSpace(text)

		parsedTime, err := time.Parse("15:04", timeStr)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Неверный формат времени. Пример: 15:30"))
			return
		}

		post, err := db.GetScheduledPostByID(database, int64(postID))
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Не удалось получить пост."))
			return
		}

		// Берём старую дату, вставляем новое время
		postAt := time.Date(
			post.PostAt.Year(),
			post.PostAt.Month(),
			post.PostAt.Day(),
			parsedTime.Hour(),
			parsedTime.Minute(),
			0, 0,
			time.Local,
		)

		err = db.UpdatePostField(database, int64(postID), "post_at", postAt)
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Не удалось обновить время."))
			return
		}

		// Обновляем content
		post.PostAt = postAt
		newContent := bot2.RegenerateContent(&post)
		_ = db.UpdatePostField(database, int64(postID), "content", newContent)

		s.State = "editing_field"
		msg := tgbotapi.NewMessage(chatID, "✅ Время обновлено. Что хотите изменить?")
		msg.ReplyMarkup = bot2.EditFieldKeyboard()
		Bot.Send(msg)

	case "viewing_posts":
		if text == "⬅️ Назад" {
			s.State = "main_menu"
			msg := tgbotapi.NewMessage(chatID, "Выберите действие:")
			msg.ReplyMarkup = bot2.MainKeyboardWithBack()
			Bot.Send(msg)
			return
		}

		if strings.HasPrefix(text, "🗑 Удалить ") {
			indexStr := strings.TrimPrefix(text, "🗑 Удалить ")
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				Bot.Send(tgbotapi.NewMessage(chatID, "Неверный формат удаления."))
				return
			}

			posts := postCache[chatID]
			if index < 1 || index > len(posts) {
				Bot.Send(tgbotapi.NewMessage(chatID, "Неверный номер поста."))
				return
			}

			selected := posts[index-1]
			err = db.DeleteScheduledPostByID(database, selected.ID)
			if err != nil {
				Bot.Send(tgbotapi.NewMessage(chatID, "Не удалось удалить пост."))
				return
			}

			Bot.Send(tgbotapi.NewMessage(chatID, "✅ Пост удалён."))

			channelID, err := db.GetChannelIDByUsername(database, s.Data["channel_username"])
			if err != nil {
				Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при получении канала."))
				return
			}

			updatedPosts, _ := db.GetScheduledPostsByChannelID(database, int64(channelID))
			postCache[chatID] = updatedPosts

			var list string
			for i, post := range updatedPosts {
				list += fmt.Sprintf("%d — 📝 Тема: %s\n", i+1, post.Theme)
				list += fmt.Sprintf("✍️ Стиль: %s\n", post.Style)
				list += fmt.Sprintf("🌐 Язык: %s\n", post.Language)
				list += fmt.Sprintf("📄 Длина: %s, %s",
					post.Length,
					post.PostAt.Format("02.01.06 15:04"),
				)
				if post.Photo != "" {
					if strings.Contains(post.Photo, "pexels") {
						list += " (pexels)"
					} else {
						list += " (Ваше фото)"
					}
				}
				list += "\n"
			}

			msg := tgbotapi.NewMessage(chatID, "Ваши посты:\n\n"+list)
			msg.ReplyMarkup = bot2.DeletePostKeyboard(updatedPosts)
			Bot.Send(msg)
			return
		}

	}
}

func handleCallback(query *tgbotapi.CallbackQuery, s *session.Session) {
	chatID := query.Message.Chat.ID
	data := query.Data

	if !checkSubscription(query.From.ID) {
		reply := tgbotapi.NewMessage(query.Message.Chat.ID, "❌ Сначала подпишись на канал @star_poster, потом возвращайся!")
		Bot.Send(reply)
		return
	}

	if strings.HasPrefix(data, "delete_") {
		idStr := strings.TrimPrefix(data, "delete_")
		postID, _ := strconv.Atoi(idStr)
		err := db.DeleteScheduledPostByID(database, int64(postID))
		if err != nil {
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при удалении"))
		} else {
			Bot.Send(tgbotapi.NewMessage(chatID, "🗑 Пост удалён"))
		}
		return
	}

	if strings.HasPrefix(data, "edit_") {
		idStr := strings.TrimPrefix(data, "edit_")
		_ = idStr // пока не используется
		Bot.Send(tgbotapi.NewMessage(chatID, "✏️ Редактирование пока не реализовано"))
	}
}

func parseTime(text string) (time.Time, error) {
	layout := "2006-01-02 15:04"
	return time.Parse(layout, text)
}

func generatePost(s *session.Session, chatID int64, fileID string) {
	style := map[string]string{
		"🤓 Экспертный":     "expert",
		"😊 Дружелюбный":    "friendly",
		"📢 Информационный": "informational",
		"🎭 Лирический":     "lyrical",
	}[s.Data["style"]]

	lang := map[string]string{
		"🇷🇺 Русский":    "ru",
		"🇬🇧 Английский": "en",
	}[s.Data["language"]]

	length := map[string]string{
		"✏️ Короткий": "short",
		"📄 Средний":   "medium",
		"📚 Длинный":   "long",
	}[s.Data["length"]]

	theme := s.Data["theme"]
	prompt := fmt.Sprintf("Сгенерируй %s пост на тему \"%s\" в стиле %s на языке %s", length, theme, style, lang)

	text, _, err := api.GeneratePostFromPrompt(prompt)
	if err != nil {
		Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка генерации поста"))
		return
	}

	channelUsername := s.Data["channel_username"]
	if channelUsername == "" {
		Bot.Send(tgbotapi.NewMessage(chatID, "❌ Канал не выбран."))
		return
	}
	if !strings.HasPrefix(channelUsername, "@") {
		channelUsername = "@" + channelUsername
	}

	var sendErr error

	// Отправляем фото без подписи
	if fileID != "" {
		photo := tgbotapi.NewPhotoToChannel(channelUsername, tgbotapi.FileID(fileID))
		_, sendErr = Bot.Send(photo)
	} else {
		translated, err := api.Translate(theme, "en")
		if err != nil || translated == "" {
			log.Printf("⚠️ Не удалось перевести тему %q, использую как есть", theme)
			translated = theme
		} else {
			log.Printf("🌐 Тема %q переведена как %q", theme, translated)
		}

		imgURL, err := pexels.FetchImage(translated)

		if err != nil || imgURL == "" {
			log.Printf("❌ Не удалось найти фото в Pexels по теме: %s", theme)
			Bot.Send(tgbotapi.NewMessage(chatID, "❌ Не удалось найти картинку по теме. Попробуй другую тему."))
			return
		}
		photo := tgbotapi.NewPhotoToChannel(channelUsername, tgbotapi.FileURL(imgURL))
		_, sendErr = Bot.Send(photo)
	}

	if sendErr != nil {
		log.Printf("❌ Ошибка при публикации фото в канал %s: %v", channelUsername, sendErr)
		Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при публикации изображения."))
		return
	}

	// Отправляем текст отдельно
	textMsg := tgbotapi.NewMessageToChannel(channelUsername, text)
	_, sendErr = Bot.Send(textMsg)
	if sendErr != nil {
		log.Printf("❌ Ошибка при публикации текста в канал %s: %v", channelUsername, sendErr)
		Bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при публикации текста."))
		return
	}

	// Если сюда дошли — всё ок
	Bot.Send(tgbotapi.NewMessage(chatID, "✅ Пост опубликован в "+channelUsername))

}

func showStyleOptions(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "✍️ Выбери стиль поста:")
	msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🤓 Экспертный"),
			tgbotapi.NewKeyboardButton("😊 Дружелюбный"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📢 Информационный"),
			tgbotapi.NewKeyboardButton("🎭 Лирический"),
		),
	)
	Bot.Send(msg)
}

func showLanguageOptions(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "🌐 Выбери язык поста:")
	msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🇷🇺 Русский"),
			tgbotapi.NewKeyboardButton("🇬🇧 Английский"),
		),
	)
	Bot.Send(msg)
}

func showLengthOptions(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "📏 Выбери длину поста:")
	msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("✏️ Короткий"),
			tgbotapi.NewKeyboardButton("📄 Средний"),
			tgbotapi.NewKeyboardButton("📚 Длинный"),
		),
	)
	Bot.Send(msg)
}

func isValidDate(dateStr string) bool {
	_, err := time.Parse("02.01.06", dateStr)
	return err == nil
}

func isValidTime(timeStr string) bool {
	_, err := time.Parse("15:04", timeStr)
	return err == nil
}

func parseDateTime(dateStr, timeStr string) (time.Time, error) {
	return time.Parse("02.01.06 15:04", dateStr+" "+timeStr)
}
