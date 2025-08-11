package main

import (
	"github.com/joho/godotenv"
	"log"
	"mybot/sub"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mybot/autopost"
	"mybot/bot"
	"mybot/db"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("❌ Не удалось загрузить .env файл")
	}

	log.Println("[DEBUG] DB_USER =", os.Getenv("DB_USER"))
	log.Println("[DEBUG] DB_PASSWORD =", os.Getenv("DB_PASSWORD"))

	telegramToken := os.Getenv("TELEGRAM_TOKEN")
	if telegramToken == "" {
		log.Fatal("❌ TELEGRAM_TOKEN не найден в окружении")
	}

	groqKey := os.Getenv("GROQ_API_KEY")
	if groqKey == "" {
		log.Fatal("❌ GROQ_API_KEY не найден в окружении")
	}

	botAPI, err := tgbotapi.NewBotAPI(telegramToken)
	if err != nil {
		log.Fatalf("❌ Ошибка запуска бота: %v", err)
	}
	botAPI.Debug = true
	log.Printf("✅ Бот авторизован как %s", botAPI.Self.UserName)

	sqlDB := db.Connect()
	log.Println("✅ Подключено к базе данных")

	err = db.Migrate(sqlDB)

	if err != nil {
		log.Fatalf("❌ Ошибка при миграции базы данных: %v", err)
	}
	log.Println("✅ Миграции выполнены")

	sub.SetDB(sqlDB)
	sub.StartTonWatcher(botAPI, sqlDB)
	autopost.Start(botAPI, sqlDB)
	bot.SetupHandlers(botAPI, sqlDB)
}
