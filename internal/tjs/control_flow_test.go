package tjs

import (
	"fmt"
	"strings"
	"testing"
)

// controlFlowHost is intentionally empty: these programs only exercise the
// compiler and VM's control-flow and value-update paths.
type controlFlowHost struct{}

func (controlFlowHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, nil
}

func runControlFlow(t *testing.T, storage, source string, budget uint64) (Value, error) {
	t.Helper()
	program, err := compile(storage, source)
	if err != nil {
		return VoidValue, err
	}
	return NewVM().Execute(&program, controlFlowHost{}, &budget)
}

func assertControlFlowInteger(t *testing.T, storage, source string, budget uint64, want int64) {
	t.Helper()
	got, err := runControlFlow(t, storage, source, budget)
	if err != nil {
		t.Fatalf("execute %s: %v", storage, err)
	}
	if !got.StrictEqual(IntegerValue(want)) {
		t.Fatalf("execute %s = %v (%s), want exact integer %d", storage, got, got.TypeName(), want)
	}
}

func TestControlFlowRustFixturePostfixAndPrefix(t *testing.T) {
	assertControlFlowInteger(t, "postfix_and_prefix.tjs", `var a=1; return (a++)*100+(++a)*10+a;`, 100, 133)
}

func TestControlFlowSwitchOrdinaryFallthrough(t *testing.T) {
	assertControlFlowInteger(t, "switch-fallthrough.tjs", `
var n = 0;
switch ("2") {
case 1:
    n = 99;
    break;
case 2:
    n += 2;
case 3:
    n += 3;
    break;
default:
    n = 88;
}
return n;
`, 300, 5)
}

func TestControlFlowSwitchBreakPreservesEnclosingBlockLocals(t *testing.T) {
	assertControlFlowInteger(t, "switch-break-enclosing-locals.tjs", `
function f() {
    for (var i = 0; i < 1; i++) {
        var posx = 0, wid = 0;
        switch (i) {
        case 0: {
            posx = 20;
            wid = 52;
        } break;
        }
        return posx + wid;
    }
}
return f();
`, 300, 72)
}
func TestControlFlowSwitchDefaultMiddleFallsThrough(t *testing.T) {
	assertControlFlowInteger(t, "switch-default-middle.tjs", `
var n = 0;
switch (0) {
case 1:
    n = 1;
    break;
default:
    n += 10;
case 2:
    n += 2;
    break;
case 3:
    n += 3;
}
return n;
`, 300, 12)
}

func TestControlFlowSwitchNoMatch(t *testing.T) {
	assertControlFlowInteger(t, "switch-no-match.tjs", `
var n = 4;
switch (9) {
case 1:
    n = 1;
case 2:
    n = 2;
}
return n;
`, 200, 4)
}

func TestControlFlowSwitchFallthroughContinueAndBreakBindToNearestLoop(t *testing.T) {
	assertControlFlowInteger(t, "switch-fallthrough-loop-control.tjs", `
var sum = 0;
for (var i = 0; i < 3; i++) {
    switch (i) {
    case 0:
        sum += 1;
    case 1:
        sum += 10;
        continue;
    default:
        sum += 100;
        break;
    }
    sum += 1000;
}
return sum;
`, 500, 1121)
}

func TestControlFlowMemberTargetPrefixAndPostfix(t *testing.T) {
	assertControlFlowInteger(t, "member-update.tjs", `
var a = [1];
var postfix = a[0]++;
var prefix = ++a[0];
return postfix * 100 + prefix * 10 + a[0];
`, 200, 133)
}

func TestControlFlowForContinueAndBreak(t *testing.T) {
	assertControlFlowInteger(t, "for-control.tjs", `var n=0; for(var i=0;i<8;i++){if(i==2)continue; if(i==5)break; n+=i;} return n;`, 300, 8)
}

func TestControlFlowNestedLoopUsesNearestControl(t *testing.T) {
	assertControlFlowInteger(t, "nested-loop-control.tjs", `
var outer = 0;
var inner = 0;
for (var i = 0; i < 3; i++) {
    for (var j = 0; j < 3; j++) {
        if (j == 1) continue;
        inner++;
        if (i == 1) break;
    }
    outer++;
}
return outer * 100 + inner;
`, 600, 305)
}

func TestControlFlowSwitchBreakAndContinueBindToNearestLoop(t *testing.T) {
	assertControlFlowInteger(t, "switch-loop-control.tjs", `
var sum = 0;
for (var i = 0; i < 4; i++) {
    switch (i) {
    case 0:
        sum += 1;
        break;
    case 1:
        sum += 10;
        continue;
    case 2:
        sum += 100;
        break;
    default:
        sum += 1000;
    }
    sum += 10000;
}
return sum;
`, 1000, 31111)
}

func TestControlFlowRejectsBreakAndContinueOutsideLoops(t *testing.T) {
	for _, tc := range []struct {
		name string
		word string
	}{
		{name: "break", word: "break"},
		{name: "continue", word: "continue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := fmt.Sprintf("%s-outside.tjs", tc.word)
			_, err := compile(storage, tc.word+";")
			if err == nil {
				t.Fatalf("compile %s unexpectedly succeeded", tc.word)
			}
			want := fmt.Sprintf("%s:1:%d: %s outside loop", storage, len(tc.word)+2, tc.word)
			if err.Error() != want {
				t.Fatalf("compile %s error = %q, want %q", tc.word, err, want)
			}
		})
	}
}

func TestControlFlowInfiniteLoopExhaustsExecutionBudget(t *testing.T) {
	storage := "infinite-loop.tjs"
	_, err := runControlFlow(t, storage, "for (;;) {}", 5)
	if err == nil {
		t.Fatal("infinite loop unexpectedly completed")
	}
	want := storage + ":1:1: execution budget exhausted after 5 instructions"
	if err.Error() != want {
		t.Fatalf("infinite loop error = %q, want %q", err, want)
	}
}

func TestControlFlowTryCatchNormalPathContinues(t *testing.T) {
	assertControlFlowInteger(t, "try-catch-normal-path.tjs", `
var n = 0;
try {
    n = 1;
} catch (e) {
    n = 100;
}
n += 2;
return n;
`, 100, 3)
}

func TestControlFlowPostfixIfConditionsAndAssignment(t *testing.T) {
	assertControlFlowInteger(t, "postfix-if-assignment.tjs", `
var n = 0;
n = 10 if true;
n = 20 if false;
return n;
`, 200, 10)
}

func TestControlFlowPostfixIfPropertyWriteAndConstruction(t *testing.T) {
	assertControlFlowInteger(t, "postfix-if-property.tjs", `
var value = 0;
var object = %[field:0];
property p { getter() { return value; } setter(v) { value = v; } }
object.field = &p;
*(&object.field) = 29 if true;
*(&object.field) = 100 if false;
new Missing() if false;
return *(&object.field);
`, 300, 29)
}

func TestControlFlowPostfixIfCallsOnlyWhenTrue(t *testing.T) {
	assertControlFlowInteger(t, "postfix-if-call.tjs", `
var calls = 0;
function mark() { calls++; }
mark() if false;
mark() if true;
return calls;
`, 300, 1)
}

func TestControlFlowPostfixIfNestedWithPrefixIfElse(t *testing.T) {
	assertControlFlowInteger(t, "postfix-if-nested.tjs", `
var n = 0;
if (true) {
    n++ if true;
    if (false) n = 100;
    else n += 2;
} else n = 200;
if (false) {
    n = 300;
} else {
    n += 3 if true;
}
return n;
`, 400, 6)
}

func TestControlFlowPostfixIfRequiresConditionAndStatementTerminator(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "missing condition",
			source: "var n = 0; n = 1 if ;",
			want:   `unsupported TJS expression token ";"`,
		},
		{
			name:   "missing semicolon before continuation",
			source: "var n = 0; n = 1 if true n = 2;",
			want:   "expected semicolon; unsupported statement continuation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compile("postfix-if-error.tjs", tc.source)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile error = %v, want message containing %q", err, tc.want)
			}
		})
	}
}
