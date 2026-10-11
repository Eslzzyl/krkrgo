package runtime

import "testing"

func TestDateNativeSupportsBookmarkTimestamps(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()

	if _, err := session.Evaluate("bookmarkDate = new Date()"); err != nil {
		t.Fatalf("construct Date: %v", err)
	}
	if _, err := session.Evaluate("bookmarkDate.getTime()"); err != nil {
		t.Fatalf("read Date timestamp: %v", err)
	}
	if _, err := session.Evaluate("bookmarkDate.setTime(1234567890)"); err != nil {
		t.Fatalf("set Date timestamp: %v", err)
	}
	value, err := session.Evaluate("bookmarkDate.getTime()")
	if err != nil {
		t.Fatalf("read updated Date timestamp: %v", err)
	}
	if got, err := value.Int(); err != nil || got != 1234567890 {
		t.Fatalf("Date.getTime() = %d, %v; want 1234567890", got, err)
	}
}
