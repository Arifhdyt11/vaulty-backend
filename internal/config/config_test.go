package config

import "testing"

func TestLoadEmbeddingEndpoint(t *testing.T) {
	cases := []struct {
		name                 string
		env                  map[string]string
		wantBaseURL, wantKey string
	}{
		{
			name:        "tanpa override ikut server chat",
			env:         map[string]string{"OPENAI_API_KEY": "ollama", "OPENAI_BASE_URL": "http://localhost:11434/v1/"},
			wantBaseURL: "http://localhost:11434/v1", wantKey: "ollama",
		},
		{
			name: "override ke 9router",
			env: map[string]string{
				"OPENAI_API_KEY": "k-hermes", "OPENAI_BASE_URL": "http://localhost:8642/v1",
				"OPENAI_EMBEDDING_API_KEY": "k-9router", "OPENAI_EMBEDDING_BASE_URL": "http://localhost:20128/v1/",
			},
			wantBaseURL: "http://localhost:20128/v1", wantKey: "k-9router",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://x")
			for _, k := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_EMBEDDING_API_KEY", "OPENAI_EMBEDDING_BASE_URL"} {
				t.Setenv(k, tc.env[k])
			}
			c, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if c.OpenAIEmbeddingBaseURL != tc.wantBaseURL || c.OpenAIEmbeddingAPIKey != tc.wantKey {
				t.Errorf("embedding = %q, %q; want %q, %q", c.OpenAIEmbeddingBaseURL, c.OpenAIEmbeddingAPIKey, tc.wantBaseURL, tc.wantKey)
			}
		})
	}
}

func TestTelegramUsers(t *testing.T) {
	got, err := telegramUsers(" 111:Arif@Mail.com , 222:b@x.id")
	if err != nil || len(got) != 2 || got[111] != "arif@mail.com" || got[222] != "b@x.id" {
		t.Fatalf("telegramUsers = %v, %v", got, err)
	}
	for _, bad := range []string{"abc:a@b.com", "111", "111:"} {
		if _, err := telegramUsers(bad); err == nil {
			t.Errorf("telegramUsers(%q) harus error", bad)
		}
	}
}
