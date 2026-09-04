package notify

import (
	"testing"
)

func TestEmailNotifierValidate(t *testing.T) {
	config := &EmailConfig{
		SMTPHost: "smtp.example.com",
		SMTPPort: 587,
		Username: "user@example.com",
		Password: "password",
		From:     "from@example.com",
		To:       "to@example.com",
	}

	if !config.IsValid() {
		t.Error("Valid config should pass validation")
	}
}

func TestEmailNotifierInvalidConfig(t *testing.T) {
	config := &EmailConfig{
		SMTPHost: "",
	}

	if config.IsValid() {
		t.Error("Invalid config should fail validation")
	}
}

func TestGotifyNotifierValidate(t *testing.T) {
	config := &GotifyConfig{
		URL:      "https://gotify.example.com",
		AppToken: "test-token",
		Priority: 5,
	}

	if !config.IsValid() {
		t.Error("Valid config should pass validation")
	}
}

func TestGotifyNotifierInvalidConfig(t *testing.T) {
	config := &GotifyConfig{
		URL: "",
	}

	if config.IsValid() {
		t.Error("Invalid config should fail validation")
	}
}

func TestWebhookNotifierValidate(t *testing.T) {
	config := &WebhookConfig{
		URL:     "https://webhook.example.com/notify",
		Headers: map[string]string{"Authorization": "Bearer token"},
	}

	if !config.IsValid() {
		t.Error("Valid config should pass validation")
	}
}

func TestWebhookNotifierInvalidConfig(t *testing.T) {
	config := &WebhookConfig{
		URL: "",
	}

	if config.IsValid() {
		t.Error("Invalid config should fail validation")
	}
}

func TestNotificationMessage(t *testing.T) {
	msg := &Message{
		Title:   "Test Notification",
		Content: "This is a test message",
	}

	if msg.Title != "Test Notification" {
		t.Errorf("Title = %v, want 'Test Notification'", msg.Title)
	}
	if msg.Content != "This is a test message" {
		t.Errorf("Content = %v", msg.Content)
	}
}

func TestNotificationResult(t *testing.T) {
	result := &Result{
		Success: true,
		Error:   "",
	}

	if !result.Success {
		t.Error("Result should be successful")
	}

	result = &Result{
		Success: false,
		Error:   "connection refused",
	}

	if result.Success {
		t.Error("Result should not be successful")
	}
	if result.Error != "connection refused" {
		t.Errorf("Error = %v, want 'connection refused'", result.Error)
	}
}

func TestChannelType(t *testing.T) {
	tests := []struct {
		channel Channel
		want    string
	}{
		{ChannelEmail, "email"},
		{ChannelGotify, "gotify"},
		{ChannelWebhook, "webhook"},
		{ChannelQQBot, "qqbot"},
	}

	for _, tt := range tests {
		if string(tt.channel) != tt.want {
			t.Errorf("Channel = %v, want %v", tt.channel, tt.want)
		}
	}
}

func TestFormatMessage(t *testing.T) {
	msg := &Message{
		Title:   "Test",
		Content: "Content",
	}

	formatted := FormatMessage(msg)
	if formatted == "" {
		t.Error("FormatMessage should not return empty string")
	}
}

func TestParseChannels(t *testing.T) {
	channels := ParseChannels("email,gotify,webhook")

	if len(channels) != 3 {
		t.Errorf("Channels count = %d, want 3", len(channels))
	}
	if channels[0] != ChannelEmail {
		t.Errorf("First channel = %v, want email", channels[0])
	}
}

func TestParseChannelsEmpty(t *testing.T) {
	channels := ParseChannels("")

	if len(channels) != 0 {
		t.Errorf("Empty string should return empty channels, got %d", len(channels))
	}
}
