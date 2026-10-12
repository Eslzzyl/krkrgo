package runtime

import (
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func setKAGEntityTestMember(t *testing.T, vm *tjs.VM, object tjs.Value, name string, value tjs.Value) {
	t.Helper()
	objectValue, ok := vm.Objects.Lookup(object.ObjectRef().Object)
	if !ok {
		t.Fatal("test object is missing")
	}
	if err := objectValue.Set(name, value); err != nil {
		t.Fatal(err)
	}
}

func kagEntityTestVM(t *testing.T) *tjs.VM {
	t.Helper()
	return tjs.NewVM()
}

func setKAGEntityTestGlobal(t *testing.T, vm *tjs.VM, name string, value tjs.Value) {
	t.Helper()
	vm.Globals[name] = value
}

func setKAGEntityTestObject(t *testing.T, vm *tjs.VM, name string, members map[string]tjs.Value) {
	t.Helper()
	object, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range members {
		setKAGEntityTestMember(t, vm, object, key, value)
	}
	setKAGEntityTestGlobal(t, vm, name, object)
}

func setKAGEntityTestEvaluator(p *KAGParser, vm *tjs.VM, host tjs.Host) {
	p.SetExpressionValueEvaluator(func(expression string) (tjs.Value, error) {
		program, err := tjs.CompileExpression(p.StorageName(), expression)
		if err != nil {
			return tjs.VoidValue, err
		}
		var budget uint64 = 1000
		return vm.Execute(&program, host, &budget)
	})
}

func TestKAGEntityAttributesResolveOrderedValuesAndKeepMetadata(t *testing.T) {
	parser, err := NewKAGParser("entity.ks", "[link literal=\"`&not_entity\" value=\"&number + 1\" value=second escaped=\"`&literal\"]")
	if err != nil {
		t.Fatal(err)
	}
	vm := kagEntityTestVM(t)
	setKAGEntityTestGlobal(t, vm, "number", tjs.IntegerValue(41))
	setKAGEntityTestEvaluator(parser, vm, DefaultServices())
	budget := uint64(100)
	event, err := parser.Step(&budget)
	if err != nil {
		t.Fatal(err)
	}
	if event.Tag == nil || event.Tag.Name != "link" {
		t.Fatalf("event = %#v, want link tag", event)
	}
	attributes := event.Tag.Attributes
	if len(attributes) != 4 {
		t.Fatalf("attributes = %#v", attributes)
	}
	want := []struct {
		name, value string
		entity      bool
	}{
		{"literal", "&not_entity", false},
		{"value", "42", true},
		{"value", "second", false},
		{"escaped", "&literal", false},
	}
	for i, expected := range want {
		got := attributes[i]
		if got.Name != expected.name || got.Value != expected.value || got.Entity != expected.entity {
			t.Errorf("attribute %d = %#v, want %#v", i, got, expected)
		}
	}
}

func TestKAGEntityAttributeErrorsIdentifyTagAndAttribute(t *testing.T) {
	parser, err := NewKAGParser("broken.ks", `[link storage="&(" target="ok"]`)
	if err != nil {
		t.Fatal(err)
	}
	vm := kagEntityTestVM(t)
	setKAGEntityTestEvaluator(parser, vm, DefaultServices())
	budget := uint64(100)
	_, err = parser.Step(&budget)
	if err == nil || !strings.Contains(err.Error(), "broken.ks:1:") || !strings.Contains(err.Error(), "KAG tag [link] attribute storage expression") {
		t.Fatalf("Step error = %v, want source-located link storage expression error", err)
	}
}

func TestKAGEntityNavigationResolvesDynamicStorageAndTarget(t *testing.T) {
	parser, err := NewKAGParser("caller.ks", `[call storage="setting.ks" target="*entry"]`)
	if err != nil {
		t.Fatal(err)
	}
	vm := kagEntityTestVM(t)
	setKAGEntityTestObject(t, vm, "sf", map[string]tjs.Value{"chapter": tjs.StringValue("1")})
	setKAGEntityTestObject(t, vm, "f", map[string]tjs.Value{
		"suffixks":    tjs.StringValue(".ks"),
		"callbackpos": tjs.StringValue("*callback"),
	})
	setKAGEntityTestEvaluator(parser, vm, DefaultServices())
	var loaded []string
	parser.SetCallbacks(&KAGCallbacks{
		OnScenarioLoad: func(_ *KAGParser, storage, _ string) (string, error) {
			loaded = append(loaded, storage)
			switch storage {
			case "setting.ks":
				return `*entry
[return storage="&sf.chapter + f.suffixks" target="&f.callbackpos"]`, nil
			case "1.ks":
				return `*callback
returned
[end]`, nil
			default:
				return "", nil
			}
		},
	})
	var text []string
	parser.SetTextCallback(func(_ *KAGParser, value string) error {
		text = append(text, value)
		return nil
	})
	budget := uint64(1000)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0] != "setting.ks" || loaded[1] != "1.ks" {
		t.Fatalf("loaded storage sequence = %#v, want setting.ks then 1.ks", loaded)
	}
	if len(text) != 1 || text[0] != "returned" {
		t.Fatalf("text events = %#v, want [returned]", text)
	}
}
