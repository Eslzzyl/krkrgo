package runtime

import "testing"

func TestVarfileStorageReadWriteAndListOctets(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	for _, source := range []string{
		`bucket = new Dictionary(); bucket.seed = <% 01 02 FF %>; Plugins.link("varfile.dll");`,
		`TextStream.write("var://./bucket/written", "bytes");`,
	} {
		if _, err := session.executeSource("<varfile-storage-test>", source, false); err != nil {
			t.Fatal(err)
		}
	}
	value, err := session.Evaluate(`TextStream.read("var://./bucket/written")`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "bytes" {
		t.Fatalf("varfile read = %q, want bytes", value.Text())
	}
	value, err = session.Evaluate(`Storages.isExistentStorage("var://./bucket/seed")`)
	if err != nil {
		t.Fatal(err)
	}
	if value.IntegerValue() != 1 {
		t.Fatalf("varfile existence = %s, want 1", value.Text())
	}
	value, err = session.Evaluate(`Storages.getListAt("var://./bucket/").join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "seed,written" {
		t.Fatalf("varfile list = %q, want seed,written", value.Text())
	}
}

func TestVarfileStorageRequiresLinkedPluginAndOctetFiles(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.Evaluate(`TextStream.read("var://./missing")`); err == nil {
		t.Fatal("unlinked varfile storage unexpectedly opened")
	}
	if _, err := session.executeSource("<varfile-non-octet-test>", `bucket = new Dictionary(); bucket.text = "not an Octet"; Plugins.link("varfile.dll");`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`TextStream.read("var://./bucket/text")`); err == nil {
		t.Fatal("varfile accepted a non-Octet value")
	}
}
