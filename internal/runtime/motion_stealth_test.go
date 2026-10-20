package runtime

import "testing"

// TestMotionPlayerConsumesStealthMotionOnLoad pins the stealth tail of
// Player::onFindMotion: the next motion load records the active chara and
// clears the pending stealth motion (PlayerMotionLoad.cpp:135-140).
func TestMotionPlayerConsumesStealthMotionOnLoad(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.executeSource("<motion-stealth>", `
		Plugins.link("motionplayer.dll");
		rm = new Motion.ResourceManager(null, 1024);
		p = new Motion.Player(rm);
		p.chara = "chara";
		p.onFindMotion("stealth", Motion.PlayFlagStealth);
		pendingBefore = p.stealthMotion;
		p.onFindMotion("idle");
		pendingAfter = p.stealthMotion;
		stealthChara = p.stealthChara;
	`, false); err != nil {
		t.Fatalf("drive stealth motion: %v", err)
	}
	value, err := session.Evaluate(`pendingBefore + "|" + pendingAfter + "|" + stealthChara`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "stealth||chara"; got != want {
		t.Fatalf("stealth state = %q, want %q", got, want)
	}
}
