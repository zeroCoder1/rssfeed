package main

import (
	"database/sql"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB
var templates *template.Template

type PageData struct {
	Articles []Article
	Article  Article
	Feeds    []Feed
	Active   string
	Query    string
}

func safeHTML(content string) template.HTML {
	log.Printf("[DEBUG] safeHTML called with content length: %d", len(content))
	if strings.Contains(content, "\uFFFD") {
		log.Printf("[WARN] Content contains replacement character (�)")
		content = strings.ReplaceAll(content, "\uFFFD", "")
	}
	if len(content) > 20 && isBinaryOrGarbled(content) {
		log.Printf("[ERROR] Content appears garbled, replacing with error message")
		return template.HTML("<div class='error-message'><p>Sorry, we couldn't properly display this article.</p><p>The article might be behind a paywall or requires JavaScript.</p><p><a href='' class='text-blue-500'>Try viewing the original article</a></p></div>")
	}
	return template.HTML(content)
}

func main() {
	var err error
	if _, err := os.Stat("/app/data"); os.IsNotExist(err) {
		os.Mkdir("/app/data", 0755)
	}
	db, err = sql.Open("sqlite3", "/app/data/suprnews.db")
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()
	if err := initDB(db); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	tmpl := template.New("base").Funcs(template.FuncMap{
		"safeHTML": safeHTML,
	})
	templates = template.Must(tmpl.ParseFiles(
		"templates/base.html",
		"templates/index.html",
		"templates/article.html",
		"templates/feeds.html",
		"templates/login.html",
		"templates/register.html",
	))
	log.Printf("Defined templates after loading: %v", templates.DefinedTemplates())
	if t := templates.Lookup("base"); t == nil {
		log.Fatal("base template not found")
	}
	go runBackgroundTasks()
	http.HandleFunc("/", requireLogin(homeHandler))
	http.HandleFunc("/article/", requireLogin(articleHandler))
	http.HandleFunc("/feeds", requireLogin(feedsHandler))
	http.HandleFunc("/feeds/add", requireLogin(addFeedHandler))
	http.HandleFunc("/feeds/delete/", requireLogin(deleteFeedHandler))
	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/register", registerHandler)
	http.HandleFunc("/logout", logoutHandler)
	http.HandleFunc("/search", requireLogin(searchHandler))
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	log.Println("Server starting on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("Server failed:", err)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	// Only handle the root path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Get all query parameters at once
	queryParams := r.URL.Query()
	feedID := queryParams.Get("feed")
	category := queryParams.Get("category")

	// Debug logging to help trace the category parameter
	log.Printf("[DEBUG] homeHandler called with feedID: '%s', category: '%s'", feedID, category)

	// Use a single function to get filtered articles
	articles, err := getFilteredArticles(db, feedID, category)
	if err != nil {
		log.Printf("[ERROR] Error getting articles: %v", err)
		http.Error(w, "Failed to load articles", http.StatusInternalServerError)
		return
	}

	log.Printf("[DEBUG] Retrieved %d articles", len(articles))

	// If articles were found, sample a few to check categories
	if len(articles) > 0 {
		sampleSize := min(3, len(articles))
		log.Printf("[DEBUG] Sample of retrieved articles:")
		for i := 0; i < sampleSize; i++ {
			log.Printf("[DEBUG] Article %d: ID=%d, Title=%s, Category=%s",
				i+1, articles[i].ID, articles[i].Title, articles[i].Category)
		}
	}

	feeds, err := getFeeds(db)
	if err != nil {
		log.Printf("[ERROR] Error getting feeds: %v", err)
		http.Error(w, "Failed to load feeds", http.StatusInternalServerError)
		return
	}

	renderTemplate(w, "index.html", PageData{
		Articles: articles,
		Feeds:    feeds,
		Active:   "home",
		Query:    category, // Pass the category to the template
	})
}

func articleHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/article/"):]
	log.Printf("[DEBUG] articleHandler: Fetching article with ID: %s", id)

	article, err := getArticleByID(db, id)
	if err != nil {
		log.Printf("[ERROR] Article not found: %v", err)
		http.Error(w, "Article not found", http.StatusNotFound)
		return
	}

	log.Printf("[DEBUG] Retrieved article: %s, URL: %s", article.Title, article.URL)
	log.Printf("[DEBUG] Initial summary length: %d", len(article.Summary))

	// Fetch and parse full content always for better results
	log.Printf("[DEBUG] Fetching full content from URL: %s", article.URL)
	content := fetchArticleContent(article.URL)

	if content != "No content available" && !isBinaryOrGarbled(content) {
		article.Summary = content
		log.Printf("[DEBUG] Updated article summary with fresh content")
	} else if isBinaryOrGarbled(article.Summary) {
		// If current summary is also garbled, use a fallback message
		log.Printf("[WARN] Both fetched content and existing summary are garbled")
		article.Summary = "<p>Content couldn't be properly displayed. <a href='" +
			article.URL + "' target='_blank' class='text-blue-500'>View the original article</a>.</p>"
	}

	// Log preview of actual content being sent to template
	if len(article.Summary) > 500 {
		log.Printf("[DEBUG] Summary preview (first 500 chars): %s", article.Summary[:500])
	} else {
		log.Printf("[DEBUG] Full summary: %s", article.Summary)
	}

	data := PageData{
		Article: article,
		Active:  "article",
	}

	log.Printf("[DEBUG] Rendering article %s", id)
	// Use the standalone article.html template directly
	w.Header().Set("Content-Type", "text/html")
	if err := templates.ExecuteTemplate(w, "article.html", data); err != nil {
		log.Printf("[ERROR] Template error: %v", err)
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
	log.Printf("[DEBUG] Successfully rendered article %s", id)
}

func feedsHandler(w http.ResponseWriter, r *http.Request) {
	feeds, err := getFeeds(db)
	if err != nil {
		http.Error(w, "Failed to load feeds: "+err.Error(), http.StatusInternalServerError)
		return
	}
	data := PageData{
		Feeds:  feeds,
		Active: "feeds",
	}
	w.Header().Set("Content-Type", "text/html")
	if err := templates.ExecuteTemplate(w, "feeds.html", data); err != nil {
		log.Printf("Template execution error: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

func addFeedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.FormValue("name")
	url := r.FormValue("url")
	if name == "" || url == "" {
		http.Error(w, "Name and URL are required", http.StatusBadRequest)
		return
	}
	if err := addFeed(db, name, url); err != nil {
		http.Error(w, "Failed to add feed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Parse the newly added feed immediately
	if err := parseRSSFeeds(db); err != nil {
		log.Printf("Error parsing newly added feed: %v", err)
	}
	http.Redirect(w, r, "/feeds", http.StatusSeeOther)
}

func deleteFeedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Path[len("/feeds/delete/"):]
	if err := deleteFeed(db, id); err != nil {
		http.Error(w, "Failed to delete feed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/feeds", http.StatusSeeOther)
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	articles, err := searchArticles(db, query)
	if err != nil {
		log.Printf("Search error: %v", err)
		http.Error(w, "Failed to search articles", http.StatusInternalServerError)
		return
	}

	feeds, err := getFeeds(db)
	if err != nil {
		log.Printf("Error getting feeds: %v", err)
		http.Error(w, "Failed to load feeds", http.StatusInternalServerError)
		return
	}

	renderTemplate(w, "index.html", PageData{
		Articles: articles,
		Feeds:    feeds,
		Active:   "search",
		Query:    query,
	})
}

// Helper function to render templates with proper error handling
func renderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	w.Header().Set("Content-Type", "text/html")
	if err := templates.ExecuteTemplate(w, tmpl, data); err != nil {
		log.Printf("Template execution error (%s): %v", tmpl, err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Login handler called with method: %s, URL: %s", r.Method, r.URL.Path)
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html")
		if err := templates.ExecuteTemplate(w, "login.html", nil); err != nil {
			log.Printf("Error rendering login template: %v", err)
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	authenticated, err := authenticateUser(db, username, password)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if !authenticated {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:  "session",
		Value: username,
		Path:  "/",
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func registerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html")
		if err := templates.ExecuteTemplate(w, "register.html", nil); err != nil {
			log.Printf("Error rendering register template: %v", err)
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	if username == "" || password == "" {
		http.Error(w, "Username and password are required", http.StatusBadRequest)
		return
	}
	err := createUser(db, username, password)
	if err != nil {
		log.Printf("Error creating user: %v", err)
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "Username already exists", http.StatusConflict)
		} else {
			http.Error(w, "Failed to create account", http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func requireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
