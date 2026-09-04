package crawler

import "testing"

func TestParseCurlCommand_GET(t *testing.T) {
	cmd := `curl 'https://api.example.com/data' -H 'Cookie: session=abc' -H 'Authorization: Bearer token123'`
	result, err := ParseCurlCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Method != "GET" {
		t.Errorf("expected GET, got %s", result.Method)
	}
	if result.URL != "https://api.example.com/data" {
		t.Errorf("unexpected URL: %s", result.URL)
	}
	if result.Headers["Cookie"] != "session=abc" {
		t.Errorf("unexpected Cookie header: %v", result.Headers["Cookie"])
	}
	if result.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("unexpected Auth header: %v", result.Headers["Authorization"])
	}
}

func TestParseCurlCommand_POST(t *testing.T) {
	cmd := `curl 'https://api.example.com/submit' -X POST -H 'Content-Type: application/json' --data '{"key":"value"}'`
	result, err := ParseCurlCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Method != "POST" {
		t.Errorf("expected POST, got %s", result.Method)
	}
	if result.Body != `{"key":"value"}` {
		t.Errorf("unexpected body: %s", result.Body)
	}
}

func TestParseCurlCommand_Complex(t *testing.T) {
	cmd := "curl 'https://api.example.com/list?page=1' \\\n  -H 'Accept: application/json' \\\n  -H 'X-API-Key: mykey' \\\n  --data-raw '{\"filter\":\"all\"}'"
	result, err := ParseCurlCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.URL != "https://api.example.com/list?page=1" {
		t.Errorf("unexpected URL: %s", result.URL)
	}
	if result.Headers["Accept"] != "application/json" {
		t.Errorf("unexpected Accept header")
	}
	if result.Body != `{"filter":"all"}` {
		t.Errorf("unexpected body: %s", result.Body)
	}
}

func TestParseCurlCommand_Invalid(t *testing.T) {
	_, err := ParseCurlCommand("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}
