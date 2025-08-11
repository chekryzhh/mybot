package sub

import (
	"database/sql"
	"fmt"
	"log"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mybot/db"
)

// ====== НАСТРОЙКИ ======
// Меняешь цену тут одной строкой:
const TonPaymentAmount = "12"                                              // цена в TON (можно "12.5")
const TonWalletAddress = "UQA4ShIPiEIR9mTHFSNUGNCSOHQFheIC2OXyjVh22GvrgIKG" // адрес получателя

// 1 TON = 1e9 нанотонов
func toNano(dec string) string {
	dec = strings.TrimSpace(dec)
	if dec == "" {
		return ""
	}
	// точный парсинг десятичной строки без плавающей точки
	r, ok := new(big.Rat).SetString(dec)
	if !ok {
		return ""
	}
	// умножаем на 1e9
	r.Mul(r, big.NewRat(1_000_000_000, 1))

	// берём floor: Num / Denom (оба big.Int)
	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())
	i := new(big.Int).Quo(num, den) // целая часть

	return i.String()
}

// Сообщение с оплатой: адрес, комментарий, цена + кнопка открыть @wallet
func SendPaymentPrompt(bot *tgbotapi.BotAPI, chatID int64, channelUsername string) {
	user := strings.TrimPrefix(strings.TrimSpace(channelUsername), "@")

	text := fmt.Sprintf(
		"❌ У вас нет активной подписки.\n\n"+
			"Стоимость: *%s TON*.\n"+
			"Откройте кошелёк по кнопке ниже и отправьте перевод вручную.\n\n"+
			"*Адрес получателя:*\n`%s`\n"+
			"*Комментарий (обязательно):*\n`channel:@%s`\n\n"+
			"👉 Обязательно укажите *юзернейм вашего канала* (с `@`) в поле «Комментарий получателю» "+
			"ровно в таком виде:\n`channel:@yourchannel`\n\n"+
			"Важно: перевод должен быть **не меньше %s TON**. "+
			"Если сумма меньше (например, 11.9 TON), подписка *не* активируется.",
		TonPaymentAmount,
		TonWalletAddress,
		user,
		TonPaymentAmount,
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	btnOpen := tgbotapi.NewInlineKeyboardButtonURL("🧩 Открыть кошелёк @wallet", "https://t.me/wallet/start")
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnOpen))
	if _, err := bot.Send(msg); err != nil {
		log.Println("⚠️ Не удалось отправить платёжное сообщение:", err)
	}
}

// Подписка активна, если срок в будущем
func IsSubscriptionActive(ch *db.Channel) bool {
	if ch.SubscriptionUntil.IsZero() {
		return false
	}
	return ch.SubscriptionUntil.After(time.Now())
}

// Активируем/продлеваем подписку (по умолчанию 30 дней; можно через SUB_DAYS)
func ActivateSubscription(dbConn *sql.DB, channelID int, walletAddress string) error {
	ch, err := db.GetChannelByID(dbConn, channelID)
	if err != nil {
		return err
	}

	days := 30
	if d, err := strconv.Atoi(os.Getenv("SUB_DAYS")); err == nil && d > 0 {
		days = d
	}
	now := time.Now()
	if ch.SubscriptionUntil.After(now) {
		ch.SubscriptionUntil = ch.SubscriptionUntil.AddDate(0, 0, days)
	} else {
		ch.SubscriptionUntil = now.AddDate(0, 0, days)
	}
	ch.WalletAddress = walletAddress
	return db.UpdateChannel(dbConn, &ch)
}

// Для совместимости с вызовами в main.go
func SetDB(_ *sql.DB) {}

// sub/subscription.go — добавь в конец файла
func GuardActiveSubscription(bot *tgbotapi.BotAPI, dbConn *sql.DB, channelID int, channelTitle string, clientID int) bool {
	ch, err := db.GetChannelByID(dbConn, channelID)
	if err != nil {
		return false
	}
	if IsSubscriptionActive(&ch) {
		return true
	}

	var chatID int64
	_ = dbConn.QueryRow(`SELECT chat_id FROM clients WHERE id=$1`, clientID).Scan(&chatID)
	if chatID != 0 {
		bot.Send(tgbotapi.NewMessage(chatID,
			"⛔ Подписка неактивна — запланированный пост не отправлен. Продлите подписку и повторите публикацию."))
	}
	return false
}
