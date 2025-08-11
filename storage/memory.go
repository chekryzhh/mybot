package storage

import "sync"

// Добавляем модель Post
type Post struct {
	ID       int
	Theme    string
	Text     string
	ImageURL string
	Views    int
}

var (
	posts = make(map[int]Post)
	dbMu  sync.Mutex // Уникальное имя для мьютекса
)

func AddPost(p Post) {
	dbMu.Lock()
	defer dbMu.Unlock()
	posts[p.ID] = p
}

func GetPost(id int) (Post, bool) {
	dbMu.Lock()
	defer dbMu.Unlock()
	p, ok := posts[id]
	return p, ok
}
