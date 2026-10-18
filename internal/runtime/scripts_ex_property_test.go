package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestScriptsExPropGetAndPropSetFlags(t *testing.T) {
	session := newScriptsTestSession(t, nil)
	if _, err := session.Evaluate(`Plugins.link("ScriptsEx.dll")`); err != nil {
		t.Fatal(err)
	}
	_, err := session.executeSource("<scripts-ex-property-test>", `
		var dictionary = %[];
		var missingDefault = 0;
		try { Scripts.propGet(dictionary, "x"); } catch (e) { missingDefault = 1; }
		var missingSet = 0;
		try { Scripts.propSet(dictionary, "y", 1, 0); } catch (e) { missingSet = 1; }
		var missingCount = Scripts.getObjectCount(dictionary);

		var ensured = Scripts.propGet(dictionary, "x", Scripts.pfMemberEnsure);
		var ensureType = typeof ensured;
		var ensureCount = Scripts.getObjectCount(dictionary);
		var optionalType = typeof Scripts.propGet(dictionary, "absent", 0);
		var optionalCount = Scripts.getObjectCount(dictionary);

		Scripts.propSet(dictionary, "h", 2, Scripts.pfMemberEnsure | Scripts.pfHiddenMember);
		var hiddenKeys = Scripts.getObjectKeys(dictionary).join(",");
		var hiddenCount = Scripts.getObjectCount(dictionary);
		dictionary.h = 3;
		var hiddenClearedKeys = Scripts.getObjectKeys(dictionary).join(",");

		var getterCount = 0;
		class Accessor {
			var n = 3;
			property value {
				getter() { getterCount = getterCount + 1; return n; }
				setter(v) { n = v * 2; }
			}
		}
		var accessor = new Accessor();
		var accessorDefault = Scripts.propGet(accessor, "value");
		Scripts.propSet(accessor, "value", 7);
		var accessorAfterSet = Scripts.propGet(accessor, "value");
		var rawIsProperty = Scripts.propGet(accessor, "value", Scripts.pfIgnoreProp) instanceof "Property";
		var rawGetterCount = getterCount;
		Scripts.propSet(accessor, "value", 8, Scripts.pfIgnoreProp);
		var accessorReplaced = accessor.value;

		class GetterOnly {
			property p { getter() { return 1; } }
		}
		var setterDenied = 0;
		try { Scripts.propSet(new GetterOnly(), "p", 2); } catch (e) { setterDenied = 1; }

		class StaticHolder {}
		Scripts.propSet(StaticHolder, "x", 7, Scripts.pfMemberEnsure | Scripts.pfStaticMember);
		var staticClassValue = StaticHolder.x;
		var staticInstanceType = typeof (new StaticHolder()).x;

		var array = [1, 2, 3];
		var arrayLast = Scripts.propGet(array, -1);
		var arrayLength = Scripts.propGet(array, "length");
		Scripts.propSet(array, -1, 9);
		var arrayLastAfterSet = array[2];

		global.prop_missing_default = missingDefault;
		global.prop_missing_set = missingSet;
		global.prop_missing_count = missingCount;
		global.prop_ensure_type = ensureType;
		global.prop_ensure_count = ensureCount;
		global.prop_optional_type = optionalType;
		global.prop_optional_count = optionalCount;
		global.prop_hidden_keys = hiddenKeys;
		global.prop_hidden_count = hiddenCount;
		global.prop_hidden_cleared_keys = hiddenClearedKeys;
		global.prop_accessor_default = accessorDefault;
		global.prop_accessor_after_set = accessorAfterSet;
		global.prop_raw_is_property = rawIsProperty;
		global.prop_raw_getter_count = rawGetterCount;
		global.prop_accessor_replaced = accessorReplaced;
		global.prop_setter_denied = setterDenied;
		global.prop_static_class = staticClassValue;
		global.prop_static_instance_type = staticInstanceType;
		global.prop_array_last = arrayLast;
		global.prop_array_length = arrayLength;
		global.prop_array_last_after_set = arrayLastAfterSet;
	`, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"prop_missing_default":      "1",
		"prop_missing_set":          "1",
		"prop_missing_count":        "0",
		"prop_ensure_type":          "void",
		"prop_ensure_count":         "1",
		"prop_optional_type":        "void",
		"prop_optional_count":       "1",
		"prop_hidden_keys":          "x",
		"prop_hidden_count":         "2",
		"prop_hidden_cleared_keys":  "h,x",
		"prop_accessor_default":     "3",
		"prop_accessor_after_set":   "14",
		"prop_raw_is_property":      "1",
		"prop_raw_getter_count":     "2",
		"prop_accessor_replaced":    "8",
		"prop_setter_denied":        "1",
		"prop_static_class":         "7",
		"prop_static_instance_type": "undefined",
		"prop_array_last":           "3",
		"prop_array_length":         "3",
		"prop_array_last_after_set": "9",
	} {
		got, err := session.Evaluate("global." + name)
		if err != nil || got.Text() != want {
			t.Errorf("%s = %q, err=%v; want %q", name, got.Text(), err, want)
		}
	}
}

func TestScriptsExPropertyErrors(t *testing.T) {
	session := newScriptsTestSession(t, nil)
	if _, err := session.Evaluate(`Plugins.link("ScriptsEx.dll")`); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"propGet missing member":   `Scripts.propGet(%[], "x")`,
		"propSet missing member":   `Scripts.propSet(%[], "x", 1, 0)`,
		"propGet non-object":       `Scripts.propGet(17, "x")`,
		"propSet non-object":       `Scripts.propSet(17, "x", 1)`,
		"propGet missing argument": `Scripts.propGet(%[])`,
		"propSet missing value":    `Scripts.propSet(%[], "x")`,
		"safeEvalStorage no name":  `Scripts.safeEvalStorage()`,
		"safeEvalStorage bad file": `Scripts.safeEvalStorage("missing-safe.tjs")`,
	} {
		if _, err := session.Evaluate(source); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	for name, source := range map[string]string{
		"propGet missing argument": `Scripts.propGet(%[])`,
		"propSet missing value":    `Scripts.propSet(%[], "x")`,
		"safeEvalStorage no name":  `Scripts.safeEvalStorage()`,
	} {
		if _, err := session.Evaluate(source); !errors.Is(err, tjs.ErrBadParamCount) {
			t.Errorf("%s error = %v, want ErrBadParamCount", name, err)
		}
	}
	if _, err := session.Evaluate(`Scripts.propGet(%[], "x")`); !errors.Is(err, tjs.ErrMemberNotFound) {
		t.Errorf("default propGet missing-member error = %v, want ErrMemberNotFound", err)
	}
	if _, err := session.Evaluate(`Scripts.propGet(%["x" => 1], "x");`); err == nil {
		t.Error("discarded propGet data result unexpectedly succeeded")
	}
}

func TestScriptsExSafeEvalStorage(t *testing.T) {
	session := newScriptsTestSession(t, map[string][]byte{
		"safe-value.tjs":   []byte(`%["name" => "first", "n" => 3]`),
		"safe-first.tjs":   []byte(`1, 2, 3`),
		"safe-offset.tjs":  []byte(`xx%["n" => 5]`),
		"safe-context.tjs": []byte(`%["value" => value + 1]`),
		"safe-empty.tjs":   []byte(""),
		"safe-const.tjs":   scriptsTestUTF16LE(`(const)%["nested" => (const)%["x" => 1]]`),
	})
	if _, err := session.Evaluate(`Plugins.link("ScriptsEx.dll")`); err != nil {
		t.Fatal(err)
	}
	_, err := session.executeSource("<scripts-safe-eval-test>", `
		var context = %[value: 4];
		global.safe_name = Scripts.safeEvalStorage("safe-value.tjs").name;
		global.safe_first = Scripts.safeEvalStorage("safe-first.tjs");
		global.safe_offset = Scripts.safeEvalStorage("safe-offset.tjs", "o2").n;
		global.safe_context = Scripts.safeEvalStorage("safe-context.tjs", "", context).value;
		global.safe_empty_type = typeof Scripts.safeEvalStorage("safe-empty.tjs");
		global.safe_const = Scripts.safeEvalStorage("safe-const.tjs").nested.x;
		global.safe_statement = Scripts.safeEvalStorage("safe-value.tjs"); Scripts.safeEvalStorage("safe-value.tjs");
	`, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"safe_name":       "first",
		"safe_first":      "1",
		"safe_offset":     "5",
		"safe_context":    "5",
		"safe_empty_type": "void",
		"safe_const":      "1",
	} {
		got, err := session.Evaluate("global." + name)
		if err != nil || got.Text() != want {
			t.Errorf("%s = %q, err=%v; want %q", name, got.Text(), err, want)
		}
	}
	if _, err := session.Evaluate(`Scripts.safeEvalStorage("safe-offset.tjs", "o99")`); err == nil || !strings.Contains(err.Error(), "exceeds storage size") {
		t.Errorf("out-of-range safeEvalStorage offset error = %v", err)
	}
}

func TestScriptsExRehashKeepsMembersResolvable(t *testing.T) {
	session := newScriptsTestSession(t, nil)
	if _, err := session.Evaluate(`Plugins.link("ScriptsEx.dll")`); err != nil {
		t.Fatal(err)
	}
	_, err := session.executeSource("<scripts-rehash-test>", `
		var dictionary = %[];
		for (var i = 0; i < 30; i++) { dictionary["k" + i] = i; }
		Scripts.rehash();
		global.rehash_value = dictionary.k0;
		global.rehash_count = Scripts.getObjectCount(dictionary);
	`, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"rehash_value": "0",
		"rehash_count": "30",
	} {
		got, err := session.Evaluate("global." + name)
		if err != nil || got.Text() != want {
			t.Errorf("%s = %q, err=%v; want %q", name, got.Text(), err, want)
		}
	}
}
