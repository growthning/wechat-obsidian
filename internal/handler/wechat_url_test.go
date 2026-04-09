package handler

import "testing"

func TestIsVideoURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		// X/Twitter - new
		{"https://x.com/user/status/123456", true},
		{"https://twitter.com/user/status/123456", true},
		{"https://X.COM/user/status/789", true},

		// Existing platforms
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://www.bilibili.com/video/BV123", true},
		{"https://b23.tv/abc", true},
		{"https://v.douyin.com/abc", true},
		{"https://www.tiktok.com/@user/video/123", true},
		{"https://www.ixigua.com/123", true},
		{"https://weibo.com/tv/show/123", true},
		{"https://www.toutiao.com/video/123", true},

		// Non-video URLs
		{"https://www.example.com/article", false},
		{"https://mp.weixin.qq.com/s/abc", false},
		{"https://www.toutiao.com/article/123", false},
		{"https://github.com/user/repo", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := isVideoURL(tt.url)
			if got != tt.want {
				t.Errorf("isVideoURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
