package runtime

import (
	_ "embed"
	"fmt"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// coreConstantsSource mirrors the official ScriptMgnIntf.cpp script constants.
// The embedded script keeps aliases and compound values (such as
// imageTagLayerType) defined by the same TJS expressions used by Kirikiri Z.
//
//go:embed core_constants.tjs
var coreConstantsSource string

var coreConstantsBootstrap struct {
	once    sync.Once
	program tjs.Program
	err     error
}

func registerCoreConstants(vm *tjs.VM) error {
	coreConstantsBootstrap.once.Do(func() {
		coreConstantsBootstrap.program, coreConstantsBootstrap.err = tjs.Compile("<core constants>", coreConstantsSource)
	})
	if coreConstantsBootstrap.err != nil {
		return fmt.Errorf("compile core TJS constants: %w", coreConstantsBootstrap.err)
	}
	// Like the Rust runtime, keep trusted bootstrap execution separate from the
	// caller-supplied script budget.
	budget := uint64(100_000)
	if _, err := vm.Execute(&coreConstantsBootstrap.program, nil, &budget); err != nil {
		return fmt.Errorf("execute core TJS constants: %w", err)
	}
	return nil
}
