package handler

import "testing"

func TestCleanURL_BilibiliShortLink(t *testing.T) {
	// Simulate what resolveShortURL would return for b23.tv
	redirected := "https://www.bilibili.com/video/BV18YwDzSE6y?-Arouter=story&buvid=28fe072ea89c9dd8023903e213140b84&from_spmid=tm.recommend.0.0&is_story_h5=false&mid=gfuKE9P4fqb%2FtjDH2KOjmw%3D%3D&p=1&plat_id=163&share_from=ugc&share_medium=iphone&share_plat=ios&share_session_id=E5F2EBC1-9554-4E5B-87A4-EFD8A62DAC2B&share_source=COPY&share_tag=s_i&spmid=main.ugc-video-detail-vertical.0.0&timestamp=1775637266&unique_k=NYDOGjx&up_id=1483869441"

	result := cleanURL(redirected)
	t.Logf("cleanURL result: %s", result)

	// Should only keep p=1
	expected := "https://www.bilibili.com/video/BV18YwDzSE6y?p=1"
	if result != expected {
		t.Errorf("cleanURL got:\n  %s\nwant:\n  %s", result, expected)
	}
}
