package storage

import "sync"

var (
	postCache = make(map[string]string)
	cacheMu   sync.Mutex // Переименовали mu в cacheMu для уникальности
)

func GetCachedPost(theme string) (string, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	text, ok := postCache[theme]
	return text, ok
}

func CachePost(theme, text string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	postCache[theme] = text
}
