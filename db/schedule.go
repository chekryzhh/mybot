package db

import (
	"database/sql"
	"fmt"
	"time"
)

type ScheduledPost struct {
	ID        int64
	ChannelID int64
	Content   string
	PostAt    time.Time
	Theme     string
	Style     string
	Language  string
	Length    string
	Photo     string // 👈 ДОБАВЬ ЭТО
	CreatedAt time.Time
}

func CreateScheduledPost(db *sql.DB, chatID int64, content string, postAt time.Time) error {
	query := `INSERT INTO scheduled_posts (chat_id, content, post_at) VALUES ($1, $2, $3)`
	_, err := db.Exec(query, chatID, content, postAt)
	return err
}

func GetScheduledPostsByChatID(db *sql.DB, chatID int64) ([]ScheduledPost, error) {
	query := `SELECT id, chat_id, content, post_at FROM scheduled_posts WHERE channel_id = $1 ORDER BY post_at ASC`
	rows, err := db.Query(query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []ScheduledPost
	for rows.Next() {
		var p ScheduledPost
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.Content, &p.PostAt); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func DeleteScheduledPostByID(db *sql.DB, postID int64) error {
	query := `DELETE FROM scheduled_posts WHERE id = $1`
	_, err := db.Exec(query, postID)
	return err
}
func GetScheduledPostsByChannelID(db *sql.DB, channelID int64) ([]ScheduledPost, error) {
	rows, err := db.Query(`
		SELECT id, channel_id, content, post_at, theme, style, language, length, photo, created_at
		FROM scheduled_posts
		WHERE channel_id = $1
		ORDER BY post_at ASC
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []ScheduledPost
	for rows.Next() {
		var post ScheduledPost
		err := rows.Scan(&post.ID, &post.ChannelID, &post.Content, &post.PostAt,
			&post.Theme, &post.Style, &post.Language, &post.Length, &post.Photo, &post.CreatedAt)
		if err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	return posts, nil
}
func GetScheduledPostsByTime(db *sql.DB, target time.Time) ([]ScheduledPost, error) {
	rows, err := db.Query(`
		SELECT id, channel_id, content, post_at, theme, style, language, length, photo, created_at
		FROM scheduled_posts
		WHERE post_at <= $1
	`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []ScheduledPost
	for rows.Next() {
		var post ScheduledPost
		err := rows.Scan(&post.ID, &post.ChannelID, &post.Content, &post.PostAt,
			&post.Theme, &post.Style, &post.Language, &post.Length, &post.Photo, &post.CreatedAt)
		if err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	return posts, nil
}
func SaveScheduledPostFull(
	db *sql.DB,
	channelID int64,
	content string,
	postAt time.Time,
	theme string,
	style string,
	language string,
	length string,
	photo string,
) error {
	query := `
		INSERT INTO scheduled_posts (
			channel_id,
			content,
			post_at,
			theme,
			style,
			language,
			length,
			photo
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := db.Exec(query, channelID, content, postAt, theme, style, language, length, photo)
	return err
}
func UpdatePostField(db *sql.DB, postID int64, field string, value any) error {
	query := fmt.Sprintf("UPDATE scheduled_posts SET %s = $1 WHERE id = $2", field)
	_, err := db.Exec(query, value, postID)
	return err
}
func GetScheduledPostByID(db *sql.DB, postID int64) (ScheduledPost, error) {
	var post ScheduledPost
	err := db.QueryRow(`
		SELECT id, channel_id, content, post_at, theme, style, language, length, photo, created_at
		FROM scheduled_posts
		WHERE id = $1
	`, postID).Scan(
		&post.ID,
		&post.ChannelID,
		&post.Content,
		&post.PostAt,
		&post.Theme,
		&post.Style,
		&post.Language,
		&post.Length,
		&post.Photo,
		&post.CreatedAt,
	)
	return post, err
}
func GetScheduledPostsByChannel(db *sql.DB, channelID string) ([]ScheduledPost, error) {
	query := `SELECT id, channel_id, content, post_at, theme, style, language, length, photo, created_at FROM scheduled_posts WHERE channel_id = $1 ORDER BY post_at`
	rows, err := db.Query(query, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []ScheduledPost
	for rows.Next() {
		var post ScheduledPost
		if err := rows.Scan(
			&post.ID,
			&post.ChannelID,
			&post.Content,
			&post.PostAt,
			&post.Theme,
			&post.Style,
			&post.Language,
			&post.Length,
			&post.Photo,
			&post.CreatedAt,
		); err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	return posts, nil
}
