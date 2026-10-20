package runtime

import (
	"testing"
)

// TestMotionPlayerRandomUsesTJSGenerator pins Player.random() to the TJS
// generator object the reference creates with `new Math.RandomGenerator()`
// (PlayerCore.cpp:201-207,1318-1333): every draw is a number in [0,1).
func TestMotionPlayerRandomUsesTJSGenerator(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.executeSource("<motion-random>", `
		Plugins.link("motionplayer.dll");
		rm = new Motion.ResourceManager(null, 1024);
		p = new Motion.Player(rm);
	`, false); err != nil {
		t.Fatalf("link motionplayer.dll: %v", err)
	}
	value, err := session.Evaluate(`(function(){
		var min = 2, max = -1, distinct = %[], count = 0;
		for(var i = 0; i < 64; i = i + 1) {
			var v = p.random();
			if(v < min) min = v;
			if(v > max) max = v;
			if(distinct[v] === void) { distinct[v] = 1; count = count + 1; }
		}
		return (min >= 0) + "|" + (max < 1) + "|" + (count > 60) + "|" + (typeof p.random());
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "1|1|1|Real"; got != want {
		t.Fatalf("random summary = %q, want %q", got, want)
	}
}

// TestMotionPlayerGlobalAlias pins the module-scope Player class alias:
// PostRegistCallback puts the top-level class into Motion, so both names are
// the same class object (main.cpp:505-527).
func TestMotionPlayerGlobalAlias(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.executeSource("<motion-player-alias>", `
		Plugins.link("motionplayer.dll");
		sameClass = (Player === Motion.Player);
		globalIsClass = Player instanceof "Class";
		p = new Player(new Motion.ResourceManager(null, 1024));
		p.chara = "chara";
		chara = p.chara;
	`, false); err != nil {
		t.Fatalf("link motionplayer.dll: %v", err)
	}
	value, err := session.Evaluate(`sameClass + "|" + globalIsClass + "|" + chara`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "1|1|chara"; got != want {
		t.Fatalf("global Player alias = %q, want %q", got, want)
	}
}

// TestMotionPlayerRandomSurvivesCollection pins the generator root: the player
// keeps the Math.RandomGenerator in Go state, so Services.GCRoots must report
// it or a collection frees the instance while random() still uses it.
func TestMotionPlayerRandomSurvivesCollection(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.executeSource("<motion-random-gc>", `
		Plugins.link("motionplayer.dll");
		p = new Motion.Player(new Motion.ResourceManager(null, 1024));
		p.random();
	`, false); err != nil {
		t.Fatalf("link motionplayer.dll: %v", err)
	}
	if err := session.GC(); err != nil {
		t.Fatalf("collect garbage: %v", err)
	}
	value, err := session.Evaluate(`(function(){ var v = p.random(); return (v >= 0) + "|" + (v < 1) + "|" + (typeof v); })()`)
	if err != nil {
		t.Fatalf("random after collection: %v", err)
	}
	if got, want := value.Text(), "1|1|Real"; got != want {
		t.Fatalf("random after collection = %q, want %q", got, want)
	}
}
