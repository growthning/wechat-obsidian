package fetcher

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/PuerkitoBio/goquery"
	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/markusmobius/go-trafilatura"
)

// Fetcher fetches WeChat articles and converts them to Markdown.
type Fetcher struct {
	timeout   time.Duration
	maxImages int
	imageDir  string
	client    *http.Client
}

// New creates a new Fetcher.
func New(imageDir string, timeout time.Duration, maxImages int) *Fetcher {
	return &Fetcher{
		timeout:   timeout,
		maxImages: maxImages,
		imageDir:  imageDir,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// ArticleResult holds the result of fetching a WeChat article.
type ArticleResult struct {
	Title    string
	Source   string
	Content  string   // full markdown with frontmatter
	Filename string   // e.g. "articles/2026-04-07-标题前20字.md"
	Images   []string // downloaded image filenames
}

// FetchArticle fetches a WeChat article URL and returns an ArticleResult.
func (f *Fetcher) FetchArticle(url string, sendTime ...time.Time) (*ArticleResult, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching article: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	// Extract title
	title := ""
	if t := doc.Find("h1.rich_media_title").First().Text(); t != "" {
		title = strings.TrimSpace(t)
	} else if t := doc.Find("h2.rich_media_title").First().Text(); t != "" {
		title = strings.TrimSpace(t)
	} else if t := doc.Find("#activity-name").First().Text(); t != "" {
		title = strings.TrimSpace(t)
	}

	// Detect WeChat anti-bot page (环境异常/验证) — fallback to Jina Reader
	pageText := doc.Text()
	if strings.Contains(pageText, "环境异常") || strings.Contains(pageText, "完成验证") || title == "" {
		log.Printf("INFO: WeChat anti-bot detected for %s, falling back to Jina Reader", url)
		now := time.Now()
		if len(sendTime) > 0 && !sendTime[0].IsZero() {
			now = sendTime[0]
		}
		return f.fetchWithJina(url, now)
	}

	// Extract source/author
	source := ""
	if s := doc.Find(".rich_media_meta_nickname a").First().Text(); s != "" {
		source = strings.TrimSpace(s)
	} else if s := doc.Find("#js_name").First().Text(); s != "" {
		source = strings.TrimSpace(s)
	}

	// Extract body
	bodyNode := doc.Find("div#js_content").First()

	// Download images (up to maxImages)
	now := time.Now()
	if len(sendTime) > 0 && !sendTime[0].IsZero() {
		now = sendTime[0]
	}
	// Use URL hash to make filenames unique per article
	urlHash := md5.Sum([]byte(url))
	imgPrefix := now.Format("20060102") + "-" + hex.EncodeToString(urlHash[:4])
	var downloadedImages []string
	imgCount := 0

	bodyNode.Find("img").Each(func(i int, sel *goquery.Selection) {
		if imgCount >= f.maxImages {
			return
		}

		imgURL, exists := sel.Attr("data-src")
		if !exists || imgURL == "" {
			imgURL, exists = sel.Attr("src")
			if !exists || imgURL == "" {
				return
			}
		}

		imgCount++
		ext := imageExt(imgURL)
		filename := fmt.Sprintf("img-%s-%03d%s", imgPrefix, imgCount, ext)

		if err := f.DownloadToFile(imgURL, filename); err == nil {
			downloadedImages = append(downloadedImages, filename)
			// Replace the img tag src so the converter picks up the local filename
			sel.SetAttr("src", filename)
			sel.RemoveAttr("data-src")
		}
	})

	// Get inner HTML of body for conversion
	bodyHTML, err := bodyNode.Html()
	if err != nil {
		bodyHTML = ""
	}

	// Convert HTML to Markdown
	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(bodyHTML)
	if err != nil {
		return nil, fmt.Errorf("converting HTML to markdown: %w", err)
	}

	// Fix image references to wiki-link format ![[filename]]
	markdown = fixImageLinks(markdown, downloadedImages, imgPrefix)

	// Generate frontmatter
	date := now.Format("2006-01-02")
	syncedAt := now.Format(time.RFC3339)
	frontmatter := fmt.Sprintf(`---
title: "%s"
source: "%s"
url: "%s"
date: %s
synced: %s
type: wechat-article
---

`,
		escapeYAML(title),
		escapeYAML(source),
		escapeYAML(url),
		date,
		syncedAt,
	)

	content := frontmatter + markdown

	// Generate filename
	safeTitle := sanitizeFilename(truncateRunes(title, 20))
	timeStr := now.Format("1504")
	filename := fmt.Sprintf("articles/%s-%s-%s.md", date, timeStr, safeTitle)

	return &ArticleResult{
		Title:    title,
		Source:   source,
		Content:  content,
		Filename: filename,
		Images:   downloadedImages,
	}, nil
}

// FetchGenericArticle fetches a non-WeChat URL. Tries trafilatura first, then Jina Reader.
func (f *Fetcher) FetchGenericArticle(articleURL string, sendTime ...time.Time) (*ArticleResult, error) {
	now := time.Now()
	if len(sendTime) > 0 && !sendTime[0].IsZero() {
		now = sendTime[0]
	}

	// 1. Try trafilatura (local, fast)
	result, err := f.fetchWithTrafilatura(articleURL, now)
	if err == nil {
		return result, nil
	}
	log.Printf("INFO: trafilatura failed for %s: %v, trying Jina", articleURL, err)

	// 2. Fallback to Jina Reader (handles JS-rendered pages)
	result, err = f.fetchWithJina(articleURL, now)
	if err == nil {
		return result, nil
	}
	log.Printf("INFO: jina failed for %s: %v, trying yt-dlp", articleURL, err)

	// 3. Fallback to yt-dlp metadata extraction (covers 1000+ sites)
	result, err = f.fetchWithYtdlp(articleURL, now)
	if err == nil {
		return result, nil
	}
	log.Printf("INFO: yt-dlp metadata failed for %s: %v", articleURL, err)

	return nil, fmt.Errorf("all extractors failed for %s", articleURL)
}

// fetchWithTrafilatura extracts article content locally using go-trafilatura.
func (f *Fetcher) fetchWithTrafilatura(articleURL string, now time.Time) (*ArticleResult, error) {
	opts := trafilatura.Options{
		OriginalURL:   &url.URL{},
		IncludeImages: true,
		IncludeLinks:  true,
	}
	if u, err := url.Parse(articleURL); err == nil {
		opts.OriginalURL = u
	}

	req, err := http.NewRequest("GET", articleURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	result, err := trafilatura.Extract(resp.Body, opts)
	if err != nil {
		return nil, fmt.Errorf("trafilatura extract: %w", err)
	}

	if result.ContentText == "" || len(result.ContentText) < 200 {
		return nil, fmt.Errorf("content too short (%d chars)", len(result.ContentText))
	}

	// Render the clean content node to HTML, then convert to markdown
	var contentHTML string
	if result.ContentNode != nil {
		var buf bytes.Buffer
		if err := html.Render(&buf, result.ContentNode); err == nil {
			contentHTML = buf.String()
		}
	}

	var markdown string
	if contentHTML != "" {
		converter := md.NewConverter("", true, nil)
		markdown, err = converter.ConvertString(contentHTML)
		if err != nil {
			markdown = result.ContentText // fallback to plain text
		}
	} else {
		markdown = result.ContentText
	}

	title := result.Metadata.Title
	if title == "" {
		title = "untitled"
	}
	source := result.Metadata.Sitename
	if source == "" {
		source = extractDomain(articleURL)
	}

	return f.buildArticleResult(articleURL, title, source, markdown, now)
}

// fetchWithJina uses Jina Reader to render JS pages and return markdown.
func (f *Fetcher) fetchWithJina(articleURL string, now time.Time) (*ArticleResult, error) {
	jinaURL := "https://r.jina.ai/" + articleURL
	req, err := http.NewRequest("GET", jinaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating jina request: %w", err)
	}
	req.Header.Set("Accept", "text/markdown")
	req.Header.Set("X-No-Cache", "true")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jina fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jina status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading jina response: %w", err)
	}

	raw := string(body)

	jinaTitle, jinaBody := parseJinaResponse(raw)
	if len(strings.TrimSpace(jinaBody)) < 200 {
		return nil, fmt.Errorf("jina content too short")
	}

	// Extract article body using content density (no rules needed)
	markdown := extractByDensity(jinaBody)
	if len(strings.TrimSpace(markdown)) < 200 {
		return nil, fmt.Errorf("density extraction too short")
	}

	if jinaTitle == "" {
		jinaTitle = "untitled"
	}
	return f.buildArticleResult(articleURL, jinaTitle, extractDomain(articleURL), markdown, now)
}

// parseJinaResponse extracts title and body from Jina Reader's markdown response.
func parseJinaResponse(raw string) (string, string) {
	title := ""
	bodyStart := 0
	lines := strings.Split(raw, "\n")

	for i, line := range lines {
		if strings.HasPrefix(line, "Title: ") {
			title = strings.TrimPrefix(line, "Title: ")
		}
		if strings.HasPrefix(line, "Markdown Content:") {
			bodyStart = i + 1
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(lines) {
		return title, strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	}

	// No Jina header found, use raw content
	// Try to extract title from first heading
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			if title == "" {
				title = strings.TrimPrefix(line, "# ")
			}
			break
		}
	}
	return title, raw
}

// fetchWithYtdlp uses yt-dlp --dump-json to extract page metadata as a universal fallback.
// yt-dlp supports 1000+ sites and can extract title/description even for non-video pages.
func (f *Fetcher) fetchWithYtdlp(articleURL string, now time.Time) (*ArticleResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/home/growthning/.local/bin/yt-dlp",
		"--dump-json",
		"--no-download",
		"--no-playlist",
		articleURL,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp metadata: %w", err)
	}

	var meta struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Uploader    string `json:"uploader"`
		WebpageURL  string `json:"webpage_url"`
	}
	if err := json.Unmarshal(output, &meta); err != nil {
		return nil, fmt.Errorf("parsing yt-dlp json: %w", err)
	}

	content := strings.TrimSpace(meta.Description)
	if len(content) < 10 {
		return nil, fmt.Errorf("yt-dlp description too short")
	}

	title := meta.Title
	if title == "" {
		title = "untitled"
	}
	source := meta.Uploader
	if source == "" {
		source = extractDomain(articleURL)
	}

	return f.buildArticleResult(articleURL, title, source, content, now)
}

// buildArticleResult creates an ArticleResult with frontmatter, image downloads, etc.
func (f *Fetcher) buildArticleResult(articleURL, title, source, markdown string, now time.Time) (*ArticleResult, error) {
	// Download images referenced in the markdown
	urlHash := md5.Sum([]byte(articleURL))
	imgPrefix := now.Format("20060102") + "-" + hex.EncodeToString(urlHash[:4])
	var downloadedImages []string

	imgRe := regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
	imgCount := 0
	markdown = imgRe.ReplaceAllStringFunc(markdown, func(match string) string {
		if imgCount >= f.maxImages {
			return match
		}
		sub := imgRe.FindStringSubmatch(match)
		if len(sub) < 2 || !strings.HasPrefix(sub[1], "http") {
			return match
		}
		imgCount++
		ext := imageExt(sub[1])
		filename := fmt.Sprintf("img-%s-%03d%s", imgPrefix, imgCount, ext)
		if err := f.DownloadToFile(sub[1], filename); err == nil {
			downloadedImages = append(downloadedImages, filename)
			return fmt.Sprintf("![[%s]]", filename)
		}
		return match
	})

	date := now.Format("2006-01-02")
	syncedAt := now.Format(time.RFC3339)
	frontmatter := fmt.Sprintf(`---
title: "%s"
source: "%s"
url: "%s"
date: %s
synced: %s
type: web-article
---

`,
		escapeYAML(title),
		escapeYAML(source),
		escapeYAML(articleURL),
		date,
		syncedAt,
	)

	content := frontmatter + markdown

	safeTitle := sanitizeFilename(truncateRunes(title, 20))
	timeStr := now.Format("1504")
	filename := fmt.Sprintf("articles/%s-%s-%s.md", date, timeStr, safeTitle)

	return &ArticleResult{
		Title:    title,
		Source:   source,
		Content:  content,
		Filename: filename,
		Images:   downloadedImages,
	}, nil
}


// extractByDensity extracts the main content from markdown using content density.
// Long paragraphs = content, short lines = navigation/noise.
// Finds the largest continuous region of high-density blocks.
func extractByDensity(markdown string) string {
	// Split into blocks by blank lines
	blocks := splitBlocks(markdown)
	if len(blocks) == 0 {
		return markdown
	}

	// Score each block: text length (stripped of markdown syntax) as density
	scores := make([]int, len(blocks))
	for i, block := range blocks {
		text := stripMarkdownSyntax(block)
		scores[i] = len([]rune(text))
	}

	// Find the longest contiguous region with high average density
	// Use Kadane's algorithm variant: blocks with score >= threshold contribute positively
	threshold := 30 // chars — lines shorter than this are likely noise
	bestStart, bestEnd, bestSum := 0, 0, 0
	curStart, curSum := 0, 0

	for i, score := range scores {
		// Positive contribution if above threshold, negative if below
		val := score - threshold
		curSum += val
		if curSum > bestSum {
			bestSum = curSum
			bestStart = curStart
			bestEnd = i + 1
		}
		if curSum < 0 {
			curSum = 0
			curStart = i + 1
		}
	}

	if bestEnd <= bestStart {
		return markdown
	}

	result := strings.Join(blocks[bestStart:bestEnd], "\n\n")

	// Trim trailing comment/interaction lines from the end
	result = trimTrailingComments(result)

	return strings.TrimSpace(result)
}

// splitBlocks splits markdown into blocks separated by blank lines.
func splitBlocks(markdown string) []string {
	var blocks []string
	var current []string
	for _, line := range strings.Split(markdown, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				blocks = append(blocks, strings.Join(current, "\n"))
				current = nil
			}
		} else {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		blocks = append(blocks, strings.Join(current, "\n"))
	}
	return blocks
}

// stripMarkdownSyntax removes markdown syntax to get approximate text length.
func stripMarkdownSyntax(s string) string {
	// Remove image/link syntax, headings markers, bold/italic markers, list markers
	s = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`).ReplaceAllString(s, "img")
	s = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`).ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "#", "")
	s = strings.ReplaceAll(s, "> ", "")
	return strings.TrimSpace(s)
}

// trimTrailingComments removes comment/interaction lines from the end of extracted content.
func trimTrailingComments(content string) string {
	lines := strings.Split(content, "\n")
	end := len(lines)
	for end > 0 {
		trimmed := strings.TrimSpace(lines[end-1])
		if isCommentLine(trimmed) || trimmed == "" {
			end--
		} else {
			break
		}
	}
	if end <= 0 {
		return content
	}
	return strings.Join(lines[:end], "\n")
}

// isCommentLine returns true if a line looks like comment/interaction noise.
func isCommentLine(line string) bool {
	if line == "" {
		return true
	}
	// User avatars and profiles
	if strings.Contains(line, "user-avatar") || strings.Contains(line, "/c/user/") {
		return true
	}
	// Reply timestamps, like counts
	if strings.Contains(line, "回复·") || strings.Contains(line, "赞    ") {
		return true
	}
	// Interaction buttons
	markers := []string{"举报", "评论", "收藏", "转发", "分享", "点赞"}
	for _, m := range markers {
		if matched, _ := regexp.MatchString(`^`+m+`\s*\d*$`, line); matched {
			return true
		}
	}
	// Bare list markers
	if line == "*" {
		return true
	}
	// "点击展开剩余 N%", "欢迎下载APP"
	if strings.HasPrefix(line, "点击展开") || strings.Contains(line, "下载APP") {
		return true
	}
	return false
}

// extractDomain returns a clean domain name from a URL.
func extractDomain(rawURL string) string {
	if idx := strings.Index(rawURL, "://"); idx >= 0 {
		host := rawURL[idx+3:]
		if slashIdx := strings.Index(host, "/"); slashIdx >= 0 {
			host = host[:slashIdx]
		}
		host = strings.TrimPrefix(host, "www.")
		host = strings.TrimPrefix(host, "m.")
		return host
	}
	return ""
}

// DownloadToFile downloads a URL to imageDir/filename.
func (f *Fetcher) DownloadToFile(url, filename string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if err := os.MkdirAll(f.imageDir, 0o755); err != nil {
		return fmt.Errorf("creating image dir: %w", err)
	}

	destPath := filepath.Join(f.imageDir, filename)
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}

// SaveFile saves raw bytes to imageDir/filename.
func (f *Fetcher) SaveFile(filename string, data []byte) error {
	if err := os.MkdirAll(f.imageDir, 0o755); err != nil {
		return fmt.Errorf("creating image dir: %w", err)
	}
	destPath := filepath.Join(f.imageDir, filename)
	return os.WriteFile(destPath, data, 0o644)
}

// fixImageLinks replaces standard markdown image links with Obsidian wiki-link format.
// It matches patterns like ![...](img-YYYYMMDD-NNN.ext) and replaces them with ![[filename]].
func fixImageLinks(markdown string, downloadedImages []string, imgPrefix string) string {
	// Replace markdown image links that point to our downloaded images
	re := regexp.MustCompile(`!\[[^\]]*\]\((img-` + imgPrefix + `-\d+\.[a-zA-Z]+)\)`)
	return re.ReplaceAllStringFunc(markdown, func(match string) string {
		sub := re.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return fmt.Sprintf("![[%s]]", sub[1])
	})
}

// imageExt returns a file extension for an image URL, defaulting to .jpg.
func imageExt(url string) string {
	// Strip query string
	u := url
	if idx := strings.Index(u, "?"); idx >= 0 {
		u = u[:idx]
	}
	ext := strings.ToLower(filepath.Ext(u))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg":
		return ext
	}
	return ".jpg"
}

// truncateRunes truncates a string to at most maxRunes Unicode code points.
func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}

// sanitizeFilename replaces characters that are invalid in filenames with underscores.
func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)
	return replacer.Replace(s)
}

// escapeYAML escapes double quotes in a string for YAML frontmatter.
func escapeYAML(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
