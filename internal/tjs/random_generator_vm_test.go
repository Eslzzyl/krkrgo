package tjs

import "testing"

func executeRandomGeneratorScript(t *testing.T, source string) Value {
	t.Helper()
	program, err := compile("random-generator-native.tjs", source)
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	budget := uint64(100_000)
	value, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestVMRandomGeneratorReferenceSequence(t *testing.T) {
	value := executeRandomGeneratorScript(t, `
var random = new Math.RandomGenerator(0), values = [];
for (var i = 0; i < 8; i++) values.add(random.random32());
return values.join(",");
`)
	const want = "647325673,1560931061,839849605,532981786,710067128,2299476654,369570609,2869776129"
	if got := value.Text(); got != want {
		t.Fatalf("RandomGenerator seed-zero sequence = %q, want %q", got, want)
	}
}

func TestVMRandomGeneratorReferenceMethods(t *testing.T) {
	value := executeRandomGeneratorScript(t, `
var random = new Math.RandomGenerator(-1);
return random.random32() + "," + random.random63() + "," + random.random64() + "," + random.random();
`)
	const want = "93740670,6236757883156951848,3910109989525945107,0.556094604553114"
	if got := value.Text(); got != want {
		t.Fatalf("RandomGenerator methods = %q, want %q", got, want)
	}
}

func TestVMRandomGeneratorSerializeAndRestore(t *testing.T) {
	value := executeRandomGeneratorScript(t, `
var random = new Math.RandomGenerator(123), state = random.serialize();
var before = state.left + "," + state.next + "," + state.state.length + "," + state.state.substr(0, 32);
var restored = new Math.RandomGenerator(state);
return before + ";" + (random.random64() == restored.random64()) + "," + (random.random() == restored.random());
`)
	const want = "1,0,4992,800000008e53ee34f15dc728d7a3b6ac;1,1"
	if got := value.Text(); got != want {
		t.Fatalf("RandomGenerator serialized state = %q, want %q", got, want)
	}
}

func TestVMRandomGeneratorRestoreAfterTwist(t *testing.T) {
	value := executeRandomGeneratorScript(t, `
var random = new Math.RandomGenerator(123);
for (var i = 0; i < 700; i++) random.random();
var state = random.serialize(), restored = new Math.RandomGenerator(state);
return state.left + "," + state.next + "," + (random.random64() == restored.random64()) + "," + (random.random() == restored.random());
`)
	if got, want := value.Text(), "473,152,1,1"; got != want {
		t.Fatalf("RandomGenerator restored cursor = %q, want %q", got, want)
	}
}
