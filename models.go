package main

import (
	"database/sql"
	"log"
	"strings"
	"time"
)

type Feed struct {
	ID        int
	Name      string
	URL       string
	CreatedAt time.Time
}

type Article struct {
	ID          int
	Title       string
	Summary     string
	URL         string
	FeedID      int
	FeedName    string
	PublishedAt time.Time
	Category    string
	Sentiment   string
	Bias        string
	ImageURL    string
	CreatedAt   time.Time
}

func initDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS feeds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS articles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			summary TEXT,
			url TEXT NOT NULL UNIQUE,
			feed_id INTEGER NOT NULL,
			published_at DATETIME,
			category TEXT DEFAULT 'other',
			sentiment TEXT DEFAULT 'neutral',
			bias TEXT DEFAULT 'neutral',
			image_url TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (feed_id) REFERENCES feeds(id)
		);
	`)
	return err
}

func createUser(db *sql.DB, username, password string) error {
	hashedPassword, err := hashPassword(password)
	if err != nil {
		log.Printf("Error hashing password: %v", err)
		return err
	}
	_, err = db.Exec("INSERT INTO users (username, password) VALUES (?, ?)", username, hashedPassword)
	if err != nil {
		log.Printf("Error inserting user into database: %v", err)
	}
	return err
}

func authenticateUser(db *sql.DB, username, password string) (bool, error) {
	var hashedPassword string
	err := db.QueryRow("SELECT password FROM users WHERE username = ?", username).Scan(&hashedPassword)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return checkPasswordHash(password, hashedPassword), nil
}

func getFeeds(db *sql.DB) ([]Feed, error) {
	rows, err := db.Query("SELECT id, name, url, created_at FROM feeds")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var feeds []Feed
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.Name, &f.URL, &f.CreatedAt); err != nil {
			return nil, err
		}
		feeds = append(feeds, f)
	}
	return feeds, nil
}

func addFeed(db *sql.DB, name, url string) error {
	_, err := db.Exec("INSERT INTO feeds (name, url) VALUES (?, ?)", name, url)
	return err
}

func deleteFeed(db *sql.DB, id string) error {
	_, err := db.Exec("DELETE FROM feeds WHERE id = ?", id)
	if err != nil {
		return err
	}
	_, err = db.Exec("DELETE FROM articles WHERE feed_id = ?", id)
	return err
}

func getFilteredArticles(db *sql.DB, feedID, category string) ([]Article, error) {
	var query string
	var args []interface{}
	baseQuery := `
        SELECT a.id, a.title, a.summary, a.url, a.feed_id, f.name, 
               a.published_at, a.category, a.sentiment, a.bias, 
               IFNULL(a.image_url, ''), a.created_at
        FROM articles a
        JOIN feeds f ON a.feed_id = f.id
    `
	var conditions []string
	if feedID != "" {
		conditions = append(conditions, "a.feed_id = ?")
		args = append(args, feedID)
	}
	if category != "" && category != "all" {
		log.Printf("[DEBUG] Filtering by category: '%s'", category)
		conditions = append(conditions, "LOWER(a.category) = LOWER(?)")
		args = append(args, category)
	}
	if len(conditions) > 0 {
		query = baseQuery + " WHERE " + strings.Join(conditions, " AND ")
	} else {
		query = baseQuery
	}
	query += " ORDER BY a.published_at DESC LIMIT 100"
	log.Printf("[DEBUG] Executing query: %s with args: %v", query, args)
	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("[ERROR] Query failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

func scanArticles(rows *sql.Rows) ([]Article, error) {
	var articles []Article
	for rows.Next() {
		var a Article
		if err := rows.Scan(
			&a.ID, &a.Title, &a.Summary, &a.URL, &a.FeedID,
			&a.FeedName, &a.PublishedAt, &a.Category, &a.Sentiment,
			&a.Bias, &a.ImageURL, &a.CreatedAt); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Summary) == "" || strings.Contains(a.Summary, "readability-page-1") {
			a.Summary = "No summary available"
		}
		articles = append(articles, a)
	}
	return articles, nil
}

func getArticleByID(db *sql.DB, id string) (Article, error) {
	var a Article
	err := db.QueryRow(`
		SELECT a.id, a.title, a.summary, a.url, a.feed_id, f.name, a.published_at, a.category, a.sentiment, a.bias, IFNULL(a.image_url, ''), a.created_at
		FROM articles a
		JOIN feeds f ON a.feed_id = f.id
		WHERE a.id = ?
	`, id).Scan(&a.ID, &a.Title, &a.Summary, &a.URL, &a.FeedID, &a.FeedName, &a.PublishedAt, &a.Category, &a.Sentiment, &a.Bias, &a.ImageURL, &a.CreatedAt)
	return a, err
}

func searchArticles(db *sql.DB, query string) ([]Article, error) {
	rows, err := db.Query(`
        SELECT a.id, a.title, a.summary, a.url, a.feed_id, f.name, 
               a.published_at, a.category, a.sentiment, a.bias, 
               IFNULL(a.image_url, ''), a.created_at
        FROM articles a
        JOIN feeds f ON a.feed_id = f.id
        WHERE a.title LIKE ? OR a.summary LIKE ?
        ORDER BY a.published_at DESC
        LIMIT 50
    `, "%"+query+"%", "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

func cleanupOldArticles(db *sql.DB) error {
	threshold := time.Now().AddDate(0, 0, -3)
	_, err := db.Exec("DELETE FROM articles WHERE published_at < ?", threshold)
	return err
}
