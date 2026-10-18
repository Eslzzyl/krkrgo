package locales

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readCatalog(t *testing.T, name string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var messages map[string]json.RawMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		t.Fatal(err)
	}
	return messages
}

func messageText(t *testing.T, key string, raw json.RawMessage) string {
	t.Helper()
	var message string
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return message
}

func TestCatalogKeysMatch(t *testing.T) {
	en := readCatalog(t, "en.json")
	zh := readCatalog(t, "zh-Hans.json")
	for key, raw := range en {
		if strings.HasPrefix(key, "@") {
			continue
		}
		if messageText(t, key, raw) == "" {
			t.Errorf("en %q is empty", key)
		}
		translated, ok := zh[key]
		if !ok {
			t.Errorf("zh-Hans is missing %q", key)
			continue
		}
		if messageText(t, key, translated) == "" {
			t.Errorf("zh-Hans %q is empty", key)
		}
	}
	for key := range zh {
		if strings.HasPrefix(key, "@") {
			continue
		}
		if _, ok := en[key]; !ok {
			t.Errorf("zh-Hans has unknown key %q", key)
		}
	}
}

func TestChineseFormatting(t *testing.T) {
	zh := In("zh-Hans")
	if got := zh.LaunchGame(); got != "启动游戏" {
		t.Fatalf("LaunchGame() = %q", got)
	}
	if got := zh.GamesReady(2); got != "2 个游戏已就绪。" {
		t.Fatalf("GamesReady(2) = %q", got)
	}
	if got := zh.GamesAddedWithFailures("磁盘已满", 1); got != "已添加 1 个游戏。磁盘已满" {
		t.Fatalf("GamesAddedWithFailures(1) = %q", got)
	}
	if got := zh.PlaytimeSummary("2分钟", "从未", zh.LaunchesCount(3)); got != "游玩时间 2分钟 · 上次游玩 从未 · 启动 3 次" {
		t.Fatalf("PlaytimeSummary() = %q", got)
	}
}

func TestEnglishPlurals(t *testing.T) {
	en := In("en")
	if got := en.GamesReady(1); got != "1 game ready." {
		t.Fatalf("GamesReady(1) = %q", got)
	}
	if got := en.GamesReady(2); got != "2 games ready." {
		t.Fatalf("GamesReady(2) = %q", got)
	}
}
