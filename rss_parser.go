package main

import (
	"bytes"
	"database/sql"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-shiori/go-readability"
	"github.com/jdkato/prose/v2"
	"github.com/mmcdole/gofeed"
)

func runBackgroundTasks() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("Refreshing RSS feeds...")
		if err := parseRSSFeeds(db); err != nil {
			log.Println("Error refreshing feeds:", err)
		}
		if err := cleanupOldArticles(db); err != nil {
			log.Println("Error cleaning up articles:", err)
		}
	}
}

func parseRSSFeeds(db *sql.DB) error {
	log.Println("Starting parseRSSFeeds...")
	feeds, err := getFeeds(db)
	if err != nil {
		return err
	}
	log.Printf("Found %d feeds\n", len(feeds))

	fp := gofeed.NewParser()
	fp.Client = &http.Client{
		Timeout: 30 * time.Second,
	}

	for _, feed := range feeds {
		log.Printf("Parsing feed: %s\n", feed.URL)
		rss, err := fp.ParseURL(feed.URL)
		if err != nil {
			log.Printf("Error parsing feed %s: %v", feed.URL, err)
			continue
		}

		for _, item := range rss.Items {
			log.Printf("Found item: Title=%q Link=%s", item.Title, item.Link)

			pubDate := time.Now()
			if item.PublishedParsed != nil {
				pubDate = *item.PublishedParsed
			}
			if pubDate.Before(time.Now().AddDate(0, 0, -3)) {
				continue
			}

			// Check if article exists
			var exists int
			err = db.QueryRow("SELECT COUNT(*) FROM articles WHERE url = ?", item.Link).Scan(&exists)
			if err != nil || exists > 0 {
				continue
			}

			// Determine category for the article
			var category string

			// First, use any categories provided by the RSS feed
			if len(item.Categories) > 0 {
				// Join all categories and try to map them to our standard categories
				feedCategories := strings.ToLower(strings.Join(item.Categories, " "))

				// Try to map the feed categories to our standard categories
				if strings.Contains(feedCategories, "tech") {
					category = "technology"
				} else if strings.Contains(feedCategories, "polit") {
					category = "politics"
				} else if strings.Contains(feedCategories, "sport") {
					category = "sports"
				} else if strings.Contains(feedCategories, "business") || strings.Contains(feedCategories, "econ") {
					category = "business"
				} else if strings.Contains(feedCategories, "entertain") {
					category = "entertainment"
				} else if strings.Contains(feedCategories, "health") {
					category = "health"
				} else if strings.Contains(feedCategories, "science") {
					category = "science"
				} else {
					// Use our NLP-based categorization
					category = categorizeArticle(item.Title + " " + item.Description)
				}
			} else {
				// Check feed name for hints
				feedNameLower := strings.ToLower(feed.Name)
				if strings.Contains(feedNameLower, "tech") || strings.Contains(feedNameLower, "digital") {
					category = "technology"
				} else if strings.Contains(feedNameLower, "sport") {
					category = "sports"
				} else if strings.Contains(feedNameLower, "business") || strings.Contains(feedNameLower, "econ") {
					category = "business"
				} else if strings.Contains(feedNameLower, "entertain") || strings.Contains(feedNameLower, "hollywood") {
					category = "entertainment"
				} else if strings.Contains(feedNameLower, "health") {
					category = "health"
				} else if strings.Contains(feedNameLower, "science") {
					category = "science"
				} else if strings.Contains(feedNameLower, "polit") {
					category = "politics"
				} else {
					// Use our NLP-based categorization
					category = categorizeArticle(item.Title + " " + item.Description)
				}
			}

			log.Printf("Categorized article '%s' as '%s'", item.Title, category)

			// Extract the image URL from the RSS feed
			var imageURL string
			if item.Image != nil {
				imageURL = item.Image.URL
			} else if len(item.Enclosures) > 0 {
				for _, enclosure := range item.Enclosures {
					// Check if the enclosure is an image
					if strings.HasPrefix(enclosure.Type, "image/") {
						imageURL = enclosure.URL
						break
					}
				}
			}

			// Insert the article
			_, err = db.Exec(`
					INSERT INTO articles (title, summary, url, feed_id, published_at, category, sentiment, bias, image_url)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				`, item.Title, item.Description, item.Link, feed.ID, pubDate, category, "neutral", "neutral", imageURL)

			if err != nil {
				log.Printf("Error inserting article %s: %v", item.Link, err)
			}
		}
	}
	log.Println("Finished parseRSSFeeds.")
	return nil
}

// Improved categorization function using NLP
func categorizeArticle(text string) string {
	// Define category keyword weights
	categoryVectors := map[string]map[string]float64{
		"technology": {
			"tech": 1.0, "technology": 1.0, "software": 0.9, "hardware": 0.9, "programming": 0.8,
			"developer": 0.8, "code": 0.7, "app": 0.5, "application": 0.6, "digital": 0.6, "ai": 0.9,
			"artificial intelligence": 1.0, "machine learning": 0.9, "data": 0.5, "computer": 0.8,
			"internet": 0.8, "cyber": 0.8, "algorithm": 0.8, "robot": 0.8, "automation": 0.8,
		},
		"politics": {
			"politic": 1.0, "government": 0.9, "election": 0.9, "vote": 0.8, "democracy": 0.9,
			"congress": 0.9, "senate": 0.9, "parliament": 0.9, "legislation": 0.9,
			"president": 0.9, "minister": 0.9, "governor": 0.8, "democrat": 0.9, "republican": 0.9,
			"policy": 0.8, "candidate": 0.8, "campaign": 0.8, "constitution": 0.9, "diplomatic": 0.9,
			"law": 0.6, "party": 0.7, "administration": 0.8, "foreign affairs": 0.9, "domestic policy": 0.9,
			"senator": 0.9, "congressman": 0.9, "ballot": 0.9, "lobbying": 0.9,
			"bipartisan": 0.9, "filibuster": 0.9, "geopolitical": 0.9, "diplomat": 0.9,
			"referendum": 0.9, "constituency": 0.9, "impeachment": 0.9, "veto": 0.9,
		},
		"sports": {
			"sport": 1.0, "game": 0.8, "match": 0.9, "player": 0.9, "team": 0.9, "athlete": 0.9,
			"championship": 0.9, "tournament": 0.9, "league": 0.9, "football": 1.0, "soccer": 1.0,
			"basketball": 1.0, "baseball": 1.0, "tennis": 1.0, "cricket": 1.0, "hockey": 1.0,
			"olympic": 1.0, "coach": 0.9, "score": 0.9, "win": 0.7, "loss": 0.7, "ipl": 1.0,
			"goal": 0.8, "stadium": 0.9, "referee": 0.9, "umpire": 0.9, "nba": 1.0,
			"nfl": 1.0, "mlb": 1.0, "fifa": 1.0, "nhl": 1.0, "pga": 1.0, "ufc": 1.0,
			"medal": 0.8, "competition": 0.7, "trophy": 0.9, "shot": 0.6,
			"fan": 0.7, "spectator": 0.8, "goalkeeper": 1.0, "runner": 0.9, "batter": 1.0,
			"wicket": 1.0, "bowl": 0.7, "draft": 0.7, "rookie": 0.9, "playoff": 1.0,
			"penalty": 0.7, "offside": 1.0, "batting": 1.0, "bowling": 0.9, "fielding": 0.9,
			"defense": 0.6, "offense": 0.6, "quarter": 0.6, "inning": 1.0, "pitch": 0.7,
			"grand slam": 1.0, "formula one": 1.0, "f1": 1.0, "boxing": 1.0, "racing": 0.8,
			"marathon": 0.9, "touchdown": 1.0, "home run": 1.0, "slam dunk": 1.0, "free throw": 1.0,
			"hat trick": 1.0, "athletics": 0.9, "gymnastics": 1.0, "swimming": 0.8,
		},
		"business": {
			"business": 1.0, "economy": 1.0, "market": 0.9, "finance": 0.9, "stock": 0.9,
			"investment": 0.9, "company": 0.8, "industry": 0.9, "trade": 0.9, "commercial": 0.9,
			"corporate": 0.9, "entrepreneur": 0.9, "startup": 0.9, "profit": 0.9, "revenue": 0.9,
			"economic": 0.9, "financial": 0.9, "banking": 0.9, "investor": 0.9, "ceo": 0.8,
		},
		"entertainment": {
			"entertain": 1.0, "movie": 1.0, "film": 1.0, "music": 1.0, "concert": 0.9,
			"celebrity": 0.9, "actor": 0.9, "actress": 0.9, "director": 0.8, "tv": 0.9,
			"television": 0.9, "show": 0.6, "drama": 0.9, "comedy": 0.9, "hollywood": 1.0,
			"bollywood": 1.0, "star": 0.8, "singer": 0.9, "album": 0.9, "release": 0.7,
		},
		"health": {
			"health": 1.0, "medical": 1.0, "medicine": 1.0, "doctor": 0.9, "hospital": 0.9,
			"disease": 0.9, "treatment": 0.9, "cure": 0.9, "patient": 0.9, "therapy": 0.9,
			"diet": 0.9, "fitness": 0.9, "wellness": 0.9, "virus": 0.9, "pandemic": 0.9,
			"vaccine": 0.9, "symptom": 0.9, "diagnosis": 0.9, "surgery": 0.9, "prescription": 0.9,
		},
		"science": {
			"science": 1.0, "research": 0.9, "study": 0.8, "discover": 0.9, "experiment": 0.9,
			"scientist": 1.0, "laboratory": 0.9, "physics": 1.0, "chemistry": 1.0, "biology": 1.0,
			"astronomy": 1.0, "space": 0.9, "theory": 0.8, "hypothesis": 0.9, "scientific": 1.0,
			"molecule": 0.9, "atom": 0.9, "quantum": 1.0, "genetic": 0.9, "evolution": 0.9,
		},
	}

	// Tokenize and POS-tag the text using prose
	doc, err := prose.NewDocument(text)
	if err != nil {
		log.Printf("Error creating prose document: %v", err)
		return "other"
	}

	// Scoring map for all categories
	scores := make(map[string]float64)

	// Lowercased token text
	for _, tok := range doc.Tokens() {
		word := strings.ToLower(tok.Text)

		// Prioritize Nouns (common and proper)
		if tok.Tag == "NN" || tok.Tag == "NNS" || tok.Tag == "NNP" || tok.Tag == "NNPS" {
			for category, vector := range categoryVectors {
				if weight, ok := vector[word]; ok {
					scores[category] += weight
				}
			}
		}
	}

	// Return category with highest score
	var topCategory string
	var topScore float64
	for cat, score := range scores {
		if score > topScore {
			topScore = score
			topCategory = cat
		}
	}

	log.Printf("[DEBUG] Categorization scores: %v", scores)
	log.Printf("[DEBUG] Top category: %s (score: %.2f)", topCategory, topScore)

	if topCategory == "" {
		return "other"
	}
	return topCategory
}

func fetchArticleContent(urlStr string) string {
	log.Printf("[DEBUG] Starting to fetch article content from: %s", urlStr)

	// First try with readability
	content := fetchWithReadability(urlStr)

	// If content is garbled or not available, try with plain HTML parsing
	if isBinaryOrGarbled(content) || content == "No content available" {
		log.Printf("[WARN] Content appears to be garbled, trying fallback method")
		content = fetchPlainHTML(urlStr)
	}

	log.Printf("[DEBUG] Final content length: %d characters", len(content))
	return content
}

// New function to fetch and process with readability
func fetchWithReadability(urlStr string) string {
	log.Printf("[DEBUG] fetchWithReadability: Starting for URL: %s", urlStr)

	// Parse the URL string into a *url.URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		log.Printf("Error parsing URL %s: %v", urlStr, err)
		return "No content available"
	}

	// Set up a request with browser-like headers
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		log.Printf("Error creating request for URL %s: %v", urlStr, err)
		return "No content available"
	}

	// Set comprehensive browser-like headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Cache-Control", "max-age=0")

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Allow up to 10 redirects
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[ERROR] Error fetching URL %s: %v", urlStr, err)
		return "No content available"
	}
	defer resp.Body.Close()

	// Check if response was successful
	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] Request failed with status code %d for URL %s", resp.StatusCode, urlStr)
		return "No content available"
	}

	log.Printf("[DEBUG] Headers received: %v", resp.Header)

	// Read the entire body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] Error reading response body for URL %s: %v", urlStr, err)
		return "No content available"
	}

	log.Printf("[DEBUG] Raw body length: %d bytes", len(bodyBytes))

	// Log first few bytes to check encoding issues
	if len(bodyBytes) > 20 {
		log.Printf("[DEBUG] First 20 bytes: %v", bodyBytes[:20])
	}

	// Detect and handle character encoding properly
	contentType := resp.Header.Get("Content-Type")
	log.Printf("[DEBUG] Content-Type from header: %s", contentType)

	encodingName := detectEncoding(bodyBytes, contentType)
	log.Printf("[DEBUG] Detected encoding: %s", encodingName)

	var decodedBody []byte
	if encodingName != "utf-8" {
		log.Printf("[DEBUG] Converting from %s to UTF-8", encodingName)
		encoding, err := htmlindex.Get(encodingName)
		if err == nil {
			decodedBody, _, err = transform.Bytes(encoding.NewDecoder(), bodyBytes)
			if err != nil {
				log.Printf("[ERROR] Error decoding from %s: %v", encodingName, err)
				decodedBody = bodyBytes
			} else {
				log.Printf("[DEBUG] Successfully converted from %s to UTF-8", encodingName)
			}
		} else {
			log.Printf("[ERROR] Failed to get encoder for %s: %v", encodingName, err)
			decodedBody = bodyBytes
		}
	} else {
		decodedBody = bodyBytes
	}

	log.Printf("[DEBUG] Decoded body length: %d bytes", len(decodedBody))

	// Check for special characters in the decoded body
	countSpecial := countSpecialChars(decodedBody)
	log.Printf("[DEBUG] Special characters in decoded body: %d", countSpecial)

	// Check if the content is binary or otherwise problematic
	if isBinaryContent(decodedBody) {
		log.Printf("[WARN] Content appears to be binary data, not suitable for parsing")
		return "No content available"
	}

	// Parse with readability after ensuring proper encoding
	log.Printf("[DEBUG] Parsing with readability...")
	article, err := readability.FromReader(bytes.NewReader(decodedBody), parsedURL)
	if err != nil {
		log.Printf("[ERROR] Error parsing article with readability: %v", err)
		return "No content available"
	}

	log.Printf("[DEBUG] Title: %s", article.Title)
	log.Printf("[DEBUG] Content length before cleaning: %d", len(article.Content))
	// Log small preview of content before cleaning
	if len(article.Content) > 100 {
		log.Printf("[DEBUG] Content preview before cleaning: %s...", article.Content[:100])
	}

	// Process the content to remove any remaining problematic characters
	content := cleanContent(article.Content)
	log.Printf("[DEBUG] Content length after cleaning: %d", len(content))

	// Do a final check for garbled content
	if isBinaryOrGarbled(content) {
		log.Printf("[WARN] After processing, content still appears garbled")
		return "No content available"
	}

	return content
}

// New function to detect binary content
func isBinaryContent(data []byte) bool {
	// Check for common binary file signatures
	if len(data) > 4 {
		// PDF signature
		if bytes.HasPrefix(data, []byte("%PDF")) {
			return true
		}
		// ZIP, DOCX, XLSX signatures
		if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			return true
		}
		// GIF
		if bytes.HasPrefix(data, []byte("GIF8")) {
			return true
		}
		// PNG
		if bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47}) {
			return true
		}
		// JPEG
		if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
			return true
		}
	}

	// Count binary characters
	binaryCount := 0
	controlCount := 0
	sampleSize := min(len(data), 1000) // Check first 1000 bytes

	for i := 0; i < sampleSize; i++ {
		if data[i] == 0x00 {
			binaryCount++
		}
		if data[i] < 9 || (data[i] > 13 && data[i] < 32) {
			controlCount++
		}
	}

	// If more than 5% binary or control chars, likely binary
	return (binaryCount*100/sampleSize > 5) || (controlCount*100/sampleSize > 10)
}

// Check if a string appears to be garbled
func isBinaryOrGarbled(content string) bool {
	if len(content) == 0 {
		return false
	}

	// Count problematic characters
	replacementCount := strings.Count(content, "\uFFFD") // Replacement character
	weirdCount := 0

	// Check a sample of the content
	sampleSize := min(len(content), 1000)
	for _, char := range content[:sampleSize] {
		// Characters outside normal readable range and not typical punctuation/whitespace
		if (char < 32 || char > 126) && char != 10 && char != 13 && char != 9 {
			if char < 0x300 || char > 0x36F { // Exclude combining diacritical marks
				weirdCount++
			}
		}
	}

	// If more than 20% problematic characters, consider garbled
	garbledRatio := (weirdCount + replacementCount) * 100 / sampleSize
	if garbledRatio > 20 {
		log.Printf("[WARN] Content appears garbled: %d%% unusual characters", garbledRatio)
		return true
	}

	// Also check for unusual patterns of characters
	weirdSequences := []string{
		"#õ", "�", "\uFFFD", "\u0000", "‡˜'ž",
	}
	for _, seq := range weirdSequences {
		if strings.Contains(content[:min(100, len(content))], seq) {
			return true
		}
	}

	return false
}

// New function for plain HTML fetching without readability
func fetchPlainHTML(urlStr string) string {
	// Create a GET request with browser headers
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return "No content available"
	}

	// Set headers to look like a browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "No content available"
	}
	defer resp.Body.Close()

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "No content available"
	}

	// Extract main content using goquery
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return "No content available"
	}

	// Remove scripts, styles, and other elements that aren't content
	doc.Find("script, style, nav, header, footer, iframe").Remove()

	// Look for article or main content elements
	var mainContent string
	selectors := []string{"article", "main", ".article", ".content", ".post", "#content", "#main"}
	for _, selector := range selectors {
		selection := doc.Find(selector).First()
		if selection.Length() > 0 {
			html, _ := selection.Html()
			if len(html) > 200 { // Only use if substantial content found
				mainContent = html
				break
			}
		}
	}

	// If no specific content element found, use body
	if mainContent == "" {
		mainContent, _ = doc.Find("body").Html()
	}

	return "<div class=\"article-content\">" + mainContent + "</div>"
}

// Helper function to detect character encoding
func detectEncoding(content []byte, contentType string) string {
	// First check Content-Type header
	if contentType != "" {
		if strings.Contains(strings.ToLower(contentType), "charset=") {
			parts := strings.SplitN(contentType, "charset=", 2)
			if len(parts) > 1 {
				charset := strings.TrimSpace(parts[1])
				charset = strings.Split(charset, ";")[0] // Remove any additional parameters
				return strings.ToLower(charset)
			}
		}
	}

	// Then check HTML meta tags
	if len(content) > 0 {
		body := string(content)
		metaCharsetMatch := regexp.MustCompile(`(?i)<meta\s+[^>]*charset\s*=\s*['"]?([^'">\s]+)['"]?`).FindStringSubmatch(body)
		if len(metaCharsetMatch) > 1 {
			return strings.ToLower(metaCharsetMatch[1])
		}

		// Check http-equiv meta tag
		metaHttpEquivMatch := regexp.MustCompile(`(?i)<meta\s+[^>]*http-equiv\s*=\s*['"]?content-type['"]?[^>]*content\s*=\s*['"]?[^'"]*charset\s*=\s*([^'">\s]+)['"]?`).FindStringSubmatch(body)
		if len(metaHttpEquivMatch) > 1 {
			return strings.ToLower(metaHttpEquivMatch[1])
		}
	}

	// Default to UTF-8
	return "utf-8"
}

// Helper function to clean problematic characters from content
func cleanContent(content string) string {
	log.Printf("[DEBUG] cleanContent: Starting with content length: %d", len(content))

	// Check for problematic characters before cleaning
	nullChars := strings.Count(content, "\u0000")
	replacementChars := strings.Count(content, "\uFFFD")

	log.Printf("[DEBUG] Found %d null chars, %d replacement chars before cleaning",
		nullChars, replacementChars)

	// Remove null bytes, replacement characters, and other problematic sequences
	content = strings.ReplaceAll(content, "\u0000", "")
	content = strings.ReplaceAll(content, "\uFFFD", "")

	// Handle Windows-1252 common encoding issues
	content = strings.ReplaceAll(content, "\u0093", "\u201C") // Opening quote
	content = strings.ReplaceAll(content, "\u0094", "\u201D") // Closing quote
	content = strings.ReplaceAll(content, "\u0096", "\u2013") // En dash
	content = strings.ReplaceAll(content, "\u0097", "\u2014") // Em dash

	// Also clean up common binary artifacts
	content = regexp.MustCompile(`[\x00-\x09\x0B\x0C\x0E-\x1F\x7F-\x9F]`).ReplaceAllString(content, "")
	content = regexp.MustCompile(`(\#õ[0-9A-Za-z]{3,6})`).ReplaceAllString(content, "")

	// Remove sequences of weird characters
	content = regexp.MustCompile(`[^\x00-\x7F]{4,}`).ReplaceAllString(content, " ")

	// Clean up any "data:" URLs which can contain binary data
	content = regexp.MustCompile(`data:[^;]+;base64,[a-zA-Z0-9+/=]{50,}`).ReplaceAllString(content, "")

	// Process YouTube links to make them stand out
	// This adds a special class that our JavaScript will detect
	youtubeRegex := regexp.MustCompile(`(https?:\/\/)?(www\.)?(youtube\.com\/watch\?v=|youtu\.be\/)([a-zA-Z0-9_-]{11})`)
	content = youtubeRegex.ReplaceAllString(content, `<a href="$0" class="youtube-link" data-video-id="$4">$0</a>`)

	log.Printf("[DEBUG] Content length after cleaning: %d", len(content))

	// Check if content contains HTML or is just plain text
	if strings.Contains(content, "<html") || strings.Contains(content, "<body") {
		log.Printf("[DEBUG] Content seems to contain full HTML document")
	} else if strings.Contains(content, "<p") || strings.Contains(content, "<div") {
		log.Printf("[DEBUG] Content contains some HTML elements")
	} else {
		log.Printf("[DEBUG] Content seems to be plain text")
	}

	return content
}

// Helper function to count special/unusual characters in a byte slice
func countSpecialChars(data []byte) int {
	count := 0
	for _, b := range data {
		if b < 32 || b > 126 {
			count++
		}
	}
	return count
}

// Helper function for min of two ints
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
