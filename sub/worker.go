package sub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mybot/db"
)

// ---- Модели TonAPI v2 ----

type TonTransaction struct {
	Hash  string          `json:"hash"`
	Utime int64           `json:"utime"`
	InMsg json.RawMessage `json:"in_msg"` // разбираем вручную, т.к. структура «плавает»
}

type TonAPIResponse struct {
	Transactions []TonTransaction `json:"transactions"`
}

// ---- Таблицы состояния ----

const watcherStateDDL = `
CREATE TABLE IF NOT EXISTS ton_watcher_state (
  wallet TEXT PRIMARY KEY,
  last_utime BIGINT NOT NULL DEFAULT 0
);`

const paymentsDDL = `
CREATE TABLE IF NOT EXISTS ton_payments(
  hash TEXT PRIMARY KEY,
  utime BIGINT NOT NULL,
  value TEXT NOT NULL,
  source TEXT,
  comment TEXT,
  processed_at TIMESTAMPTZ DEFAULT now()
);`

func getLastUtime(dbConn *sql.DB, wallet string) (int64, error) {
	if _, err := dbConn.Exec(watcherStateDDL); err != nil {
		return 0, err
	}
	if _, err := dbConn.Exec(paymentsDDL); err != nil {
		return 0, err
	}
	var ut int64
	err := dbConn.QueryRow(`SELECT last_utime FROM ton_watcher_state WHERE wallet=$1`, wallet).Scan(&ut)
	if err == sql.ErrNoRows {
		if _, e := dbConn.Exec(`INSERT INTO ton_watcher_state(wallet,last_utime) VALUES ($1,$2)`, wallet, 0); e != nil {
			return 0, e
		}
		return 0, nil
	}
	return ut, err
}

func setLastUtime(dbConn *sql.DB, wallet string, ut int64) error {
	_, err := dbConn.Exec(`UPDATE ton_watcher_state SET last_utime=$2 WHERE wallet=$1`, wallet, ut)
	return err
}

// ---- Старт воркера ----

func StartTonWatcher(bot *tgbotapi.BotAPI, dbConn *sql.DB) {
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := checkTonTransactions(bot, dbConn); err != nil {
				log.Println("⚠️ TON watcher:", err)
			}
		}
	}()
}

// ---- Утилиты для «капризного» JSON ----

// рекурсивно ищем поле (без учёта регистра) и возвращаем его сырой JSON
func findFieldRaw(raw json.RawMessage, want string) (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		for k, v := range obj {
			if strings.EqualFold(k, want) {
				return v, true
			}
			// заглянем внутрь вложенных объектов/массивов
			if sub, ok := findFieldRaw(v, want); ok {
				return sub, true
			}
			var arr []json.RawMessage
			if err := json.Unmarshal(v, &arr); err == nil {
				for _, it := range arr {
					if sub, ok := findFieldRaw(it, want); ok {
						return sub, true
					}
				}
			}
		}
	}
	return nil, false
}

func parseNano(raw json.RawMessage) (*big.Int, error) {
	// строка
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		i := new(big.Int)
		if _, ok := i.SetString(strings.TrimSpace(s), 10); ok {
			return i, nil
		}
	}
	// число (json.Number)
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		i := new(big.Int)
		if _, ok := i.SetString(n.String(), 10); ok {
			return i, nil
		}
	}
	// int64
	var i64 int64
	if err := json.Unmarshal(raw, &i64); err == nil {
		return big.NewInt(i64), nil
	}
	return nil, fmt.Errorf("unsupported value format: %s", string(raw))
}

func parseAddress(raw json.RawMessage) string {
	// строка
	var s string
	if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	// объект — попробуем типичные ключи
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for _, k := range []string{"address", "account", "base64", "raw", "hex"} {
			if v, ok := m[k]; ok {
				if str, ok := v.(string); ok && str != "" {
					return str
				}
			}
		}
		for _, v := range m {
			if str, ok := v.(string); ok && looksLikeTonAddress(str) {
				return str
			}
		}
	}
	return ""
}

func looksLikeTonAddress(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 20 || strings.Contains(s, " ") {
		return false
	}
	return strings.HasPrefix(s, "EQ") || strings.HasPrefix(s, "UQ") || strings.Contains(s, ":")
}

var reChannel = regexp.MustCompile(`(?i)channel\s*:\s*@([a-z0-9_]{3,})`)

// ---- Основная логика ----

func checkTonTransactions(bot *tgbotapi.BotAPI, dbConn *sql.DB) error {
	apiKey := os.Getenv("TONAPI_KEY")
	wallet := TonWalletAddress
	debug := os.Getenv("DEBUG_WATCHER") == "1"

	lastU, err := getLastUtime(dbConn, wallet)
	if err != nil {
		return fmt.Errorf("getLastUtime: %w", err)
	}

	url := fmt.Sprintf("https://tonapi.io/v2/blockchain/accounts/%s/transactions?limit=50", wallet)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		time.Sleep(30 * time.Second)
		return nil
	}
	if resp.StatusCode >= 500 {
		time.Sleep(10 * time.Second)
		return fmt.Errorf("tonapi status: %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tonapi status: %d", resp.StatusCode)
	}

	var data TonAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	if debug {
		log.Printf("🔎 watcher: fetched %d tx, last_utime=%d", len(data.Transactions), lastU)
	}
	if len(data.Transactions) == 0 {
		return nil
	}

	requiredNano := new(big.Int)
	if _, ok := requiredNano.SetString(toNano(TonPaymentAmount), 10); !ok {
		return fmt.Errorf("incorrect TonPaymentAmount=%s", TonPaymentAmount)
	}

	var maxU int64 = lastU
	for _, tx := range data.Transactions {
		if tx.Utime <= lastU {
			continue
		}
		if tx.Utime > maxU {
			maxU = tx.Utime
		}

		// --- Достаём comment/value/source из любых глубин in_msg ---
		comment := ""
		if cRaw, ok := findFieldRaw(tx.InMsg, "comment"); ok {
			_ = json.Unmarshal(cRaw, &comment)
		}
		if comment == "" {
			if tRaw, ok := findFieldRaw(tx.InMsg, "text"); ok {
				_ = json.Unmarshal(tRaw, &comment)
			}
		}
		valueRaw, vOK := findFieldRaw(tx.InMsg, "value")
		sourceRaw, sOK := findFieldRaw(tx.InMsg, "source")

		if comment == "" {
			if debug {
				log.Printf("• %s: skip — empty comment (in_msg doesn’t expose text)", short(tx.Hash))
			}
			continue
		}

		// channel:@username (регистр/пробелы не важны)
		m := reChannel.FindStringSubmatch(comment)
		if m == nil {
			if debug {
				log.Printf("• %s: skip — no channel tag in comment=%q", short(tx.Hash), comment)
			}
			continue
		}
		username := strings.ToLower(m[1])
		withAt := "@" + username

		// находим канал в БД
		channelID, err := db.GetChannelIDByUsername(dbConn, username)
		if err != nil {
			channelID, err = db.GetChannelIDByUsername(dbConn, withAt)
		}
		if err != nil {
			if debug {
				log.Printf("• %s: skip — channel %q not found", short(tx.Hash), withAt)
			}
			continue
		}
		channel, err := db.GetChannelByID(dbConn, channelID)
		if err != nil {
			log.Println("❌ Ошибка получения канала:", err)
			continue
		}

		// сумма
		if !vOK {
			if debug {
				log.Printf("• %s: skip — no 'value' field in in_msg", short(tx.Hash))
			}
			continue
		}
		val, err := parseNano(valueRaw)
		if err != nil {
			if debug {
				log.Printf("• %s: skip — bad value=%s", short(tx.Hash), string(valueRaw))
			}
			continue
		}
		if val.Cmp(requiredNano) < 0 {
			// уведомим владельца
			var chatID int64
			_ = dbConn.QueryRow(`SELECT chat_id FROM clients WHERE id=$1`, channel.ClientID).Scan(&chatID)
			if chatID != 0 {
				need, _ := new(big.Rat).SetString(TonPaymentAmount)
				got := new(big.Rat).SetInt(val)
				got.Quo(got, new(big.Rat).SetInt64(1_000_000_000))
				msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
					"⚠️ Недостаточная сумма для канала %s: пришло ~%s TON, нужно ≥ %s TON.\nКомментарий: %q",
					withAt, got.FloatString(3), need.FloatString(3), comment))
				_, _ = bot.Send(msg)
			}
			if debug {
				log.Printf("• %s: skip — amount too small", short(tx.Hash))
			}
			continue
		}

		// идемпотентная фиксация
		sourceStr := ""
		if sOK {
			sourceStr = parseAddress(sourceRaw)
			if sourceStr == "" {
				_ = json.Unmarshal(sourceRaw, &sourceStr)
			}
		}
		res, err := dbConn.Exec(
			`INSERT INTO ton_payments(hash, utime, value, source, comment)
			 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
			tx.Hash, tx.Utime, string(valueRaw), sourceStr, comment,
		)
		if err != nil {
			log.Println("pay insert err:", err)
			continue
		}
		if rows, _ := res.RowsAffected(); rows == 0 {
			if debug {
				log.Printf("• %s: skip — already processed", short(tx.Hash))
			}
			continue
		}

		// уже активна?
		if IsSubscriptionActive(&channel) {
			if debug {
				log.Printf("• %s: skip — already active for %s", short(tx.Hash), withAt)
			}
			continue
		}

		if sourceStr == "" {
			sourceStr = "(unknown)"
		}
		if err := ActivateSubscription(dbConn, channelID, sourceStr); err != nil {
			log.Println("❌ Не удалось активировать подписку:", err)
			continue
		}
		log.Printf("✅ Подписка активирована для канала %s (tx=%s, кошелёк: %s)", withAt, short(tx.Hash), sourceStr)

		// уведомим владельца
		var chatID int64
		if err := dbConn.QueryRow(`SELECT chat_id FROM clients WHERE id=$1`, channel.ClientID).Scan(&chatID); err == nil && chatID != 0 {
			days := 30
			if d, err := strconv.Atoi(os.Getenv("SUB_DAYS")); err == nil && d > 0 {
				days = d
			}
			_, _ = bot.Send(tgbotapi.NewMessage(chatID,
				fmt.Sprintf("✅ Подписка для канала %s активирована на %d дней!\n\n"+
					"▶️ Для начала работы отправьте мне юзернейм канала ещё раз (например, %s).",
					withAt, days, withAt)))

		}
	}

	if maxU > lastU {
		if err := setLastUtime(dbConn, wallet, maxU); err != nil {
			return fmt.Errorf("setLastUtime: %w", err)
		}
	}
	return nil
}

func short(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
