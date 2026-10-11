package kag

import "testing"

func TestParsePreservesLabelsAttributesAndUTF16Columns(t *testing.T) {
	scenario, err := Parse("test.ks", "*start|Title\n😀x[tag a=1 a=2 b=\"x] y\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := scenario.Labels["*start"]; got != 0 {
		t.Fatalf("label index = %d, want 0", got)
	}
	if got := scenario.Tokens[1].Text; got != "😀x" {
		t.Fatalf("text = %q", got)
	}
	tag := scenario.Tokens[2].Tag
	if tag == nil || tag.Name != "tag" {
		t.Fatalf("tag = %#v", tag)
	}
	if len(tag.Attributes) != 3 || tag.Attributes[0].Value != "1" || tag.Attributes[1].Value != "2" {
		t.Fatalf("attributes = %#v", tag.Attributes)
	}
	if got, ok := tag.Get("a"); !ok || got != "2" {
		t.Fatalf("last duplicate = %q, %v", got, ok)
	}
	if got := scenario.Tokens[2].Location.Column; got != 4 {
		t.Fatalf("tag UTF-16 column = %d, want 4", got)
	}
}

func TestParseAcceptsTrailingEmptyAttributeValues(t *testing.T) {
	scenario, err := Parse("empty-attributes.ks", "[draw pos= layer=]\n[draw pos=]\n[draw pos]\n")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"", "", "true"} {
		tag := scenario.Tokens[i*2].Tag
		if tag == nil || tag.Name != "draw" {
			t.Fatalf("tag %d = %#v, want draw", i, tag)
		}
		if got, ok := tag.Get("pos"); !ok || got != want {
			t.Errorf("tag %d pos = %q, %v, want %q, true", i, got, ok, want)
		}
		if i == 0 {
			if got, ok := tag.Get("layer"); !ok || got != "" {
				t.Errorf("trailing layer = %q, %v, want empty and present", got, ok)
			}
		}
	}
}

func TestParseSkipsIndentedSemicolonCommentLines(t *testing.T) {
	scenario, err := Parse("comments.ks", "*start\n\t; comment containing [notatag]\n \t; another comment\n  visible text\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(scenario.Tokens) != 3 {
		t.Fatalf("tokens = %#v, want label, visible text, and final newline", scenario.Tokens)
	}
	if got := scenario.Tokens[1].Text; got != "  visible text" {
		t.Fatalf("visible text = %q, want leading spaces preserved", got)
	}
	if scenario.Tokens[2].Kind != KindNewline {
		t.Fatalf("final token = %#v, want newline", scenario.Tokens[2])
	}
}
func TestParseMarksLeadingEntityExpressionsWithoutChangingLiteralValues(t *testing.T) {
	scenario, err := Parse("test.ks", `[link storage="&sf.chapter + f.suffixks" target=&f.callbackpos literal="`+"`"+`&literal"]`)
	if err != nil {
		t.Fatal(err)
	}
	tag := scenario.Tokens[0].Tag
	if tag == nil || tag.Name != "link" {
		t.Fatalf("tag = %#v, want link", tag)
	}
	want := []struct {
		name, value string
		entity      bool
	}{
		{"storage", "sf.chapter + f.suffixks", true},
		{"target", "f.callbackpos", true},
		{"literal", "&literal", false},
	}
	if len(tag.Attributes) != len(want) {
		t.Fatalf("attributes = %#v", tag.Attributes)
	}
	for i, expected := range want {
		got := tag.Attributes[i]
		if got.Name != expected.name || got.Value != expected.value || got.Entity != expected.entity {
			t.Errorf("attribute %d = %#v, want %s=%q (entity=%v)", i, got, expected.name, expected.value, expected.entity)
		}
	}
}

func TestParsePreservesRepeatedAnonymousLabelsAsAliases(t *testing.T) {
	scenario, err := Parse("test.ks", "*start|Title\n*|\n*|\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(scenario.Tokens) != 3 {
		t.Fatalf("tokens = %#v, want three labels", scenario.Tokens)
	}
	wantNames := []string{"*start", "*start:2", "*start:3"}
	if len(scenario.Labels) != len(wantNames) {
		t.Fatalf("labels = %#v, want three occurrence aliases", scenario.Labels)
	}
	for i, name := range wantNames {
		if got := scenario.Labels[name]; got != i {
			t.Errorf("label %q index = %d, want %d", name, got, i)
		}
		token := scenario.Tokens[i]
		if token.Kind != KindLabel || token.Label == nil || token.Label.Name != name {
			t.Errorf("label token %d = %#v, want alias %q", i, token, name)
			continue
		}
		if token.Label.Caption == nil {
			t.Errorf("label token %q lost its caption", name)
			continue
		}
		wantCaption := ""
		if i == 0 {
			wantCaption = "Title"
		}
		if *token.Label.Caption != wantCaption {
			t.Errorf("label token %q caption = %q, want %q", name, *token.Label.Caption, wantCaption)
		}
	}
}

func TestParserJumpAndCallRepeatedLabelAliases(t *testing.T) {
	scenario, err := Parse("test.ks", "*a|first\nA\n*|second\nB\n*a\nC\n")
	if err != nil {
		t.Fatal(err)
	}
	parser := NewParser(scenario)
	if err := parser.Jump("*a:2"); err != nil {
		t.Fatal(err)
	}
	if got := parser.State().Position; got != 3 {
		t.Fatalf("jump to second occurrence position = %d, want 3", got)
	}
	if err := parser.Call("a:3"); err != nil {
		t.Fatal(err)
	}
	if got := parser.State().Position; got != 6 {
		t.Fatalf("call to third occurrence position = %d, want 6", got)
	}
	if err := parser.ReturnFromCall(); err != nil {
		t.Fatal(err)
	}
	if got := parser.State().Position; got != 3 {
		t.Fatalf("return position = %d, want 3", got)
	}
	token := parser.NextToken()
	if token == nil || token.Label == nil || token.Label.Name != "*a:2" {
		t.Fatalf("second occurrence token = %#v, want *a:2 label", token)
	}
}

func TestParserCursorCallAndRestore(t *testing.T) {
	scenario, err := Parse("test.ks", "*start\nhello\n*sub\nbye")
	if err != nil {
		t.Fatal(err)
	}
	parser := NewParser(scenario)
	parser.NextToken()
	saved := parser.State()
	if err := parser.Call("sub"); err != nil {
		t.Fatal(err)
	}
	if err := parser.ReturnFromCall(); err != nil {
		t.Fatal(err)
	}
	if got := parser.State(); got.Position != saved.Position || len(got.Calls) != len(saved.Calls) {
		t.Fatalf("state after return = %#v, want %#v", got, saved)
	}
	if err := parser.Restore(Cursor{Position: 100}); err == nil {
		t.Fatal("invalid cursor restore succeeded")
	}
}

func TestParseMultilineTag(t *testing.T) {
	scenario, err := Parse("test.ks", "@line a=1 \\\n; b=2\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(scenario.Tokens) != 1 || scenario.Tokens[0].Tag == nil {
		t.Fatalf("tokens = %#v", scenario.Tokens)
	}
	if len(scenario.Tokens[0].Tag.Attributes) != 2 {
		t.Fatalf("attributes = %#v", scenario.Tokens[0].Tag.Attributes)
	}
}

func TestParserCurrentLineStringRetainsPhysicalSourceLines(t *testing.T) {
	source := "*start|Title\r\n@line value=1 \\\r\n;continued=2\r\n*next|Caption\r\nbody"
	scenario, err := Parse("lines.ks", source)
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []string{"*start|Title", "@line value=1 \\", ";continued=2", "*next|Caption", "body"}
	if len(scenario.SourceLines) != len(wantLines) {
		t.Fatalf("source lines = %#v, want %#v", scenario.SourceLines, wantLines)
	}
	for i, want := range wantLines {
		if got := scenario.SourceLines[i]; got != want {
			t.Errorf("physical line %d = %q, want %q", i+1, got, want)
		}
	}

	parser := NewParser(scenario)
	if got := parser.CurrentLineStr(); got != wantLines[0] {
		t.Fatalf("initial current line = %q, want %q", got, wantLines[0])
	}
	if token := parser.NextToken(); token == nil || token.Kind != KindLabel {
		t.Fatalf("first token = %#v, want label", token)
	}
	if got := parser.CurrentLineStr(); got != wantLines[0] {
		t.Fatalf("current line after label = %q, want %q", got, wantLines[0])
	}
	if token := parser.NextToken(); token == nil || token.Kind != KindTag {
		t.Fatalf("second token = %#v, want tag", token)
	}
	if got := parser.CurrentLineStr(); got != wantLines[1] {
		t.Fatalf("current line after multiline tag = %q, want original physical line %q", got, wantLines[1])
	}
	saved := parser.State()
	if saved.CurrentLine != 2 {
		t.Fatalf("saved current line = %d, want physical line 2", saved.CurrentLine)
	}

	if err := parser.Jump("next"); err != nil {
		t.Fatal(err)
	}
	if got := parser.CurrentLineStr(); got != wantLines[1] {
		t.Fatalf("current line after jump = %q, want %q", got, wantLines[1])
	}
	if token := parser.NextToken(); token == nil || token.Kind != KindLabel {
		t.Fatalf("label token after jump = %#v", token)
	}
	if got := parser.CurrentLineStr(); got != wantLines[1] {
		t.Fatalf("current line after jumped label = %q, want %q", got, wantLines[1])
	}
	if token := parser.NextToken(); token == nil || token.Kind != KindText {
		t.Fatalf("body token after label = %#v, want text", token)
	}
	if got := parser.CurrentLineStr(); got != wantLines[4] {
		t.Fatalf("current line after label body = %q, want %q", got, wantLines[4])
	}
	if err := parser.Restore(saved); err != nil {
		t.Fatal(err)
	}
	if got := parser.CurrentLineStr(); got != wantLines[1] {
		t.Fatalf("restored current line = %q, want %q", got, wantLines[1])
	}
}
