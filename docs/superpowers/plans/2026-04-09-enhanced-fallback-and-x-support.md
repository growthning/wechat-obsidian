# Enhanced Generic Fallback & X/Twitter Support

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve the generic URL fallback chain so difficult sites (X/Twitter, JS-heavy pages) still produce useful content in Obsidian, instead of bare link memos.

**Architecture:** Add yt-dlp metadata extraction as a 3rd-tier fallback in `FetchGenericArticle`, enrich memo fallback with WeChat link card description, add X/Twitter to video URL detection, and update the Obsidian plugin's platform display map.

**Tech Stack:** Go (backend), yt-dlp CLI (`--dump-json`), TypeScript (Obsidian plugin)

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/fetcher/article.go` | Modify | Add `fetchWithYtdlp()` as 3rd fallback in `FetchGenericArticle` |
| `internal/handler/wechat.go` | Modify | Add X to `isVideoURL()`, pass `desc` into `fetchGenericOrMemoForUser`, enrich memo fallback |
| `obsidian-plugin/src/writer.ts` | Modify | Add X/Twitter to `videoPlatform()` |

---

### Task 1: Add yt-dlp metadata extraction fallback to FetchGenericArticle

**Files:**
- Modify: `internal/fetcher/article.go:187-207` (FetchGenericArticle)
- Modify: `internal/fetcher/article.go` (add fetchWithYtdlp method)

- [ ] **Step 1: Add `fetchWithYtdlp` method to article.go**

Add this method after the existing `fetchWithJina` method (after line ~319):

```go
// fetchWithYtdlp uses yt-dlp --dump-json to extract page metadata as a universal fallback.
// yt-dlp supports 1000+ sites and can extract title/description even for non-video pages.
func (f *Fetcher) fetchWithYtdlp(articleURL string, now time.Time) (*ArticleResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "yt-dlp",
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
```

- [ ] **Step 2: Add missing imports to article.go**

Add `"context"`, `"encoding/json"`, and `"os/exec"` to the import block at the top of `article.go` (they are not currently imported there):

```go
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
```

- [ ] **Step 3: Wire yt-dlp fallback into FetchGenericArticle**

Replace the `FetchGenericArticle` method (lines 187-207) with:

```go
// FetchGenericArticle fetches a non-WeChat URL.
// Tries trafilatura first, then Jina Reader, then yt-dlp metadata extraction.
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
```

- [ ] **Step 4: Build and verify compilation**

Run:
```bash
cd /Users/liuning/claude/wechat-obsidian && go build ./...
```
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/fetcher/article.go
git commit -m "feat: add yt-dlp metadata extraction as 3rd fallback in FetchGenericArticle"
```

---

### Task 2: Enrich memo fallback with link card description

**Files:**
- Modify: `internal/handler/wechat.go:440` (pass desc to fetchGenericOrMemoForUser)
- Modify: `internal/handler/wechat.go:526-544` (fetchGenericOrMemoForUser signature + memo content)

- [ ] **Step 1: Update fetchGenericOrMemoForUser to accept desc**

Replace lines 526-544 of `wechat.go`:

```go
// fetchGenericOrMemoForUser tries to fetch a URL as an article with user ID; falls back to memo.
func (h *WeChatHandler) fetchGenericOrMemoForUser(url, title, desc, msgID string, now time.Time, userID int64) {
	result, err := h.fetcher.FetchGenericArticle(url, now)
	if err != nil {
		log.Printf("INFO: generic fetch failed for %s: %v, saving as memo", url, err)
		content := fmt.Sprintf("[%s](%s)", title, url)
		if desc != "" {
			content += "\n\n" + desc
		}
		m := &model.Message{
			MsgID:     msgID,
			Type:      "memo",
			Content:   content,
			Title:     title,
			SourceURL: url,
			CreatedAt: now,
			UserID:    userID,
		}
		if _, err2 := h.store.InsertMessage(m); err2 != nil {
			log.Printf("ERROR: inserting link memo: %v", err2)
		}
		return
	}
```

- [ ] **Step 2: Update the call site to pass desc**

Replace line 440 of `wechat.go`:

```go
// Before:
go h.fetchGenericOrMemoForUser(cleanedURL, msg.Link.Title, msg.MsgID, now, user.ID)

// After:
go h.fetchGenericOrMemoForUser(cleanedURL, msg.Link.Title, msg.Link.Desc, msg.MsgID, now, user.ID)
```

- [ ] **Step 3: Build and verify compilation**

Run:
```bash
cd /Users/liuning/claude/wechat-obsidian && go build ./...
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/handler/wechat.go
git commit -m "feat: include link card description in memo fallback"
```

---

### Task 3: Add X/Twitter to video URL detection

**Files:**
- Modify: `internal/handler/wechat.go:724-744` (isVideoURL)

- [ ] **Step 1: Add X/Twitter hosts to isVideoURL**

Replace the `isVideoURL` function (lines 723-744):

```go
// isVideoURL checks if a URL is from a known video platform.
func isVideoURL(rawURL string) bool {
	videoHosts := []string{
		"youtube.com", "youtu.be", "m.youtube.com",
		"bilibili.com", "b23.tv",
		"douyin.com", "v.douyin.com",
		"tiktok.com",
		"ixigua.com",
		"weibo.com/tv",
		"x.com", "twitter.com",
	}
	lower := strings.ToLower(rawURL)
	for _, host := range videoHosts {
		if strings.Contains(lower, host) {
			return true
		}
	}
	// Toutiao video URLs
	if strings.Contains(lower, "toutiao.com/video/") {
		return true
	}
	return false
}
```

- [ ] **Step 2: Build and verify compilation**

Run:
```bash
cd /Users/liuning/claude/wechat-obsidian && go build ./...
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/handler/wechat.go
git commit -m "feat: add X/Twitter to video URL detection"
```

---

### Task 4: Add X/Twitter to Obsidian plugin platform display

**Files:**
- Modify: `obsidian-plugin/src/writer.ts:137-145` (videoPlatform)

- [ ] **Step 1: Update videoPlatform to include X**

Replace the `videoPlatform` method (lines 137-145):

```typescript
  private videoPlatform(msg: SyncMessage): string {
    const url = (msg.source_url || msg.content || "").toLowerCase();
    if (url.includes("bilibili.com") || url.includes("b23.tv")) return "B站";
    if (url.includes("toutiao.com") || url.includes("ixigua.com")) return "头条";
    if (url.includes("youtube.com") || url.includes("youtu.be")) return "YouTube";
    if (url.includes("douyin.com")) return "抖音";
    if (url.includes("tiktok.com")) return "TikTok";
    if (url.includes("x.com") || url.includes("twitter.com")) return "X";
    return "视频";
  }
```

- [ ] **Step 2: Build the plugin**

Run:
```bash
cd /Users/liuning/claude/wechat-obsidian/obsidian-plugin && npm run build
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add obsidian-plugin/src/writer.ts
git commit -m "feat: add X/Twitter platform display in Obsidian daily notes"
```

---

### Task 5: Final build verification

- [ ] **Step 1: Full backend build**

Run:
```bash
cd /Users/liuning/claude/wechat-obsidian && go build ./...
```
Expected: no errors.

- [ ] **Step 2: Full plugin build**

Run:
```bash
cd /Users/liuning/claude/wechat-obsidian/obsidian-plugin && npm run build
```
Expected: no errors.
