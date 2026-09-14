package notify

import (
	"net/url"

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

func TestDingTalkNotifierValidate(t *testing.T) {
	config := &DingTalkConfig{
		WebhookURL: "https://oapi.dingtalk.com/robot/send?access_token=test",
		Secret:     "SECtest",
	}

	if !config.IsValid() {
		t.Error("Valid config should pass validation")
	}
}

func TestDingTalkNotifierValidateWithoutSecret(t *testing.T) {
	config := &DingTalkConfig{
		WebhookURL: "https://oapi.dingtalk.com/robot/send?access_token=test",
	}

	if !config.IsValid() {
		t.Error("Config without secret (non-signed mode) should pass validation")
	}
}

func TestDingTalkNotifierInvalidConfig(t *testing.T) {
	config := &DingTalkConfig{
		WebhookURL: "",
		Secret:     "SECtest",
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
		{ChannelDingTalk, "dingtalk"},
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

// 钉钉加签 URL：URL 上误带的旧 timestamp/sign 参数必须剥掉再重签，
// 否则双份参数让钉钉校验到旧时间戳，报 310000「机器人发送签名过期」
func TestDingTalkSignedURLStripsStaleParams(t *testing.T) {
	s := NewDingTalkSender(&DingTalkConfig{
		WebhookURL: "https://oapi.dingtalk.com/robot/send?access_token=tok&timestamp=1600000000000&sign=OLD",
		Secret:     "SEC123",
	})
	u, err := url.Parse(s.signedURL())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()

	if got := q.Get("timestamp"); got == "1600000000000" || len(got) != 13 {
		t.Errorf("stale timestamp not re-signed: %q", got)
	}
	if got := q.Get("sign"); got == "OLD" || got == "" {
		t.Errorf("stale sign not replaced: %q", got)
	}
	if got := q.Get("access_token"); got != "tok" {
		t.Errorf("access_token lost: %q", got)
	}
}

// 无 secret：仍剥旧参数（旧签名永远无效），但不附加新签名
func TestDingTalkSignedURLNoSecret(t *testing.T) {
	s := NewDingTalkSender(&DingTalkConfig{
		WebhookURL: "https://oapi.dingtalk.com/robot/send?access_token=tok&timestamp=1600000000000&sign=OLD",
	})
	got := s.signedURL()
	if got != "https://oapi.dingtalk.com/robot/send?access_token=tok" {
		t.Errorf("unexpected URL: %s", got)
	}
}
