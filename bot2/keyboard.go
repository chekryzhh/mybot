package bot2

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mybot/db"
)

// Главное меню
func MainKeyboardWithBack() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📥 Сгенерировать пост"),
			tgbotapi.NewKeyboardButton("🗓 Запланировать пост"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📋 Мои посты"),
			tgbotapi.NewKeyboardButton("✏️ Редактировать пост"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🔄 Сменить канал"),
		),
	)
}

// ✅ Стили, как в publisher.go
var Style = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🤓 Экспертный"),
		tgbotapi.NewKeyboardButton("😊 Дружелюбный"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("📢 Информационный"),
		tgbotapi.NewKeyboardButton("🎭 Лирический"),
	),
)

var Language = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🇷🇺 Русский"),
		tgbotapi.NewKeyboardButton("🇬🇧 Английский"),
	),
)

var Length = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("✏️ Короткий"),
		tgbotapi.NewKeyboardButton("📄 Средний"),
		tgbotapi.NewKeyboardButton("📚 Длинный"),
	),
)

// Клавиатура списка постов
func EditPostKeyboard(posts []db.ScheduledPost) tgbotapi.ReplyKeyboardMarkup {
	rows := [][]tgbotapi.KeyboardButton{}
	for i := range posts {
		btn := tgbotapi.NewKeyboardButton(fmt.Sprintf("✏️ Изменить %d", i+1))
		rows = append(rows, tgbotapi.NewKeyboardButtonRow(btn))
	}
	rows = append(rows, tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("⬅️ Назад"),
	))
	return tgbotapi.NewReplyKeyboard(rows...)
}

func ChannelChoiceKeyboard(channels []db.Channel) tgbotapi.ReplyKeyboardMarkup {
	rows := []tgbotapi.KeyboardButton{}
	for _, ch := range channels {
		rows = append(rows, tgbotapi.NewKeyboardButton("@"+ch.ChannelTitle))
	}
	keyboard := tgbotapi.NewReplyKeyboard(rows)
	keyboard.ResizeKeyboard = true
	return keyboard
}
func DeletePostKeyboard(posts []db.ScheduledPost) tgbotapi.ReplyKeyboardMarkup {
	var rows [][]tgbotapi.KeyboardButton
	for i := range posts {
		rows = append(rows, tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(fmt.Sprintf("🗑 Удалить %d", i+1)),
		))
	}
	rows = append(rows, tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("⬅️ Назад")))
	return tgbotapi.NewReplyKeyboard(rows...)
}
