package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	md "github.com/JohannesKaufmann/html-to-markdown"
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
func (f *Fetcher) FetchArticle(url string) (*ArticleResult, error) {
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
	// Use timestamp to make filenames unique across articles
	imgPrefix := now.Format("20060102-150405")
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
	filename := fmt.Sprintf("articles/%s-%s.md", date, safeTitle)

	return &ArticleResult{
		Title:    title,
		Source:   source,
		Content:  content,
		Filename: filename,
		Images:   downloadedImages,
	}, nil
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
