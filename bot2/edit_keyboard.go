package bot2

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Клавиатура для выбора поля редактирования
var EditFieldKeyboard = func() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📅 Изменить дату"),
			tgbotapi.NewKeyboardButton("⏰ Изменить время"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📝 Изменить тему"),
			tgbotapi.NewKeyboardButton("✍️ Изменить стиль"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🌐 Изменить язык"),
			tgbotapi.NewKeyboardButton("📏 Изменить длину"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🖼 Изменить изображение"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("⬅️ Назад"),
		),
	)
}
