package fetcher

import "testing"

func TestExtractByDensity_preservesContent(t *testing.T) {
	// Ensure the density extractor still works after our changes
	input := "This is a long paragraph that contains meaningful content about the article topic. " +
		"It should be preserved by the density extraction algorithm because it exceeds the threshold.\n\n" +
		"Another substantial paragraph with detailed information that adds value to the reader.\n\n" +
		"短"
	result := extractByDensity(input)
	if len(result) < 50 {
		t.Errorf("extractByDensity returned too short content: %q", result)
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://x.com/user/status/123", "x.com"},
		{"https://twitter.com/user/status/123", "twitter.com"},
		{"https://www.toutiao.com/video/123", "toutiao.com"},
		{"https://mp.weixin.qq.com/s/abc", "mp.weixin.qq.com"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := extractDomain(tt.url)
			if got != tt.want {
				t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
