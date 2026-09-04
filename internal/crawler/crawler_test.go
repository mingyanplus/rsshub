package crawler

import (
	"testing"
)

func TestParseRSSFeed(t *testing.T) {
	rssContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Test Feed</title>
		<link>https://example.com</link>
		<description>A test RSS feed</description>
		<item>
			<title>First Article</title>
			<link>https://example.com/article1</link>
			<description>This is the first article</description>
			<pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate>
		</item>
		<item>
			<title>Second Article</title>
			<link>https://example.com/article2</link>
			<description>This is the second article</description>
			<pubDate>Mon, 01 Jan 2024 13:00:00 +0000</pubDate>
		</item>
	</channel>
</rss>`

	feed, err := ParseFeed([]byte(rssContent))
	if err != nil {
		t.Fatalf("ParseFeed() error = %v", err)
	}

	if feed.Title != "Test Feed" {
		t.Errorf("Feed.Title = %v, want Test Feed", feed.Title)
	}
	if len(feed.Items) != 2 {
		t.Errorf("len(Feed.Items) = %v, want 2", len(feed.Items))
	}
	if feed.Items[0].Title != "First Article" {
		t.Errorf("Items[0].Title = %v, want First Article", feed.Items[0].Title)
	}
}

func TestParseAtomFeed(t *testing.T) {
	atomContent := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
	<title>Atom Feed</title>
	<link href="https://example.com"/>
	<subtitle>An Atom feed</subtitle>
	<entry>
		<title>Atom Entry</title>
		<link href="https://example.com/atom1"/>
		<summary>Atom entry summary</summary>
		<updated>2024-01-01T12:00:00Z</updated>
	</entry>
</feed>`

	feed, err := ParseFeed([]byte(atomContent))
	if err != nil {
		t.Fatalf("ParseFeed() error = %v", err)
	}

	if feed.Title != "Atom Feed" {
		t.Errorf("Feed.Title = %v, want Atom Feed", feed.Title)
	}
	if len(feed.Items) != 1 {
		t.Errorf("len(Feed.Items) = %v, want 1", len(feed.Items))
	}
}

func TestParseJSONFeed(t *testing.T) {
	jsonContent := `{
		"version": "https://jsonfeed.org/version/1",
		"title": "JSON Feed",
		"home_page_url": "https://example.com",
		"items": [
			{
				"id": "1",
				"title": "JSON Item",
				"url": "https://example.com/json1",
				"content_text": "JSON item content"
			}
		]
	}`

	feed, err := ParseFeed([]byte(jsonContent))
	if err != nil {
		t.Fatalf("ParseFeed() error = %v", err)
	}

	if feed.Title != "JSON Feed" {
		t.Errorf("Feed.Title = %v, want JSON Feed", feed.Title)
	}
	if len(feed.Items) != 1 {
		t.Errorf("len(Feed.Items) = %v, want 1", len(feed.Items))
	}
}

func TestParseInvalidFeed(t *testing.T) {
	invalidContent := `<html><body>Not a feed</body></html>`
	_, err := ParseFeed([]byte(invalidContent))
	if err == nil {
		t.Error("ParseFeed() should return error for invalid content")
	}
}

func TestExtractContent(t *testing.T) {
	item := &FeedItem{
		Title:       "Test",
		Link:        "https://example.com/test",
		Description: "Short description",
		Content:     "Full content here",
	}

	content := ExtractContent(item)
	if content != "Full content here" {
		t.Errorf("ExtractContent() = %v, want 'Full content here'", content)
	}
}

func TestExtractContentFallback(t *testing.T) {
	item := &FeedItem{
		Title:       "Test",
		Link:        "https://example.com/test",
		Description: "Short description",
	}

	content := ExtractContent(item)
	if content != "Short description" {
		t.Errorf("ExtractContent() = %v, want 'Short description'", content)
	}
}
