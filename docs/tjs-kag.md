# TJS and KAG Porting Status

## Reference and scope

The Rust tree is a migration/API reference; official C++ [`krkrz`](https://github.com/krkrz/krkrz) is the semantic and acceptance target. TJS implementation is in `internal/tjs`; KAG tokenization is in `internal/kag`, with execution/native integration in `internal/runtime`.

## TJS implementation

[`compiler.go`](../internal/tjs/compiler.go) provides scanning, parsing, preprocessing, and compilation through `Compile` and `CompileExpression`. [`vm.go`](../internal/tjs/vm.go) implements `VM`, execution, object/class and native dispatch, and budget-aware execution. Related source includes [`object.go`](../internal/tjs/object.go), [`dispatch.go`](../internal/tjs/dispatch.go), [`gc.go`](../internal/tjs/gc.go), and [`preprocessor.go`](../internal/tjs/preprocessor.go).

The implemented TJS subset includes functions, closures, rest/spread/forward arguments, exception handling, `with`, loop/switch control flow, octet literals, native/class dispatch, conditional preprocessing, bounded arrays, program validation, and serialization. It does not represent full TJS2 compatibility.

Script class declarations compile to the existing `Class`, `Function`, and `Property` descriptors. The VM supports empty classes, multiple evaluated base expressions, base-to-derived instance initialization, class-named constructors, inherited method/property lookup, `super` method/property dispatch, native-class initialization through the class dispatch path, and default parameter expressions in functions and class methods.

The Go scanner joins adjacent string literals that use the same quote delimiter, including across whitespace and newlines. Comments and differing delimiters keep literals separate; commas in arrays and call arguments and the ordinary `+` operator remain parser-level syntax.

The TJS preprocessor supports `@if`, `@elif`, `@else`, and `@endif` conditional directives in source before compilation. These are TJS preprocessor directives, not KAG scenario tags.

TJS String values expose `sprintf` using the C++ `TJSFormatString` subset: integer (`diouxX`), real (`fegEG`), and string (`cs`) conversions, flags, fixed or `*` width/precision, and escaped percent signs. Formatting and string precision follow UTF-16 code-unit semantics.
TJS `Math` is provided by the runtime native with constants and the implemented standard numeric operations. The package-level xorshift `random` generator and its seeding behavior are described in [Runtime and native services](runtime-natives.md).



### Runtime preprocessor environment

The official C++ runtime initializes the TJS preprocessor symbol `kirikiriz` to `1`, and the Rust runtime follows the same convention. Go runtime sessions now initialize the same symbol and reuse one preprocessor across `Session.ExecuteStorage` and `Scripts` storage execution, preserving definitions across source units.

### Known TJS gaps

- Session storage execution supports source TJS; compiled `TJS2` bytecode is explicitly rejected by [`scripts_native.go`](../internal/runtime/scripts_native.go).
- Registered `Scripts.compileStorage`, `Scripts.dump`, and object-valued `Scripts.setCallMissing` operations return unsupported errors: bytecode output, heap dumping, and missing-member dispatch are unavailable.
- `Scripts.textEncoding` applies to `Scripts` storage execution; `Session.ExecuteStorage` retains its separate automatic-decoding behavior.
- Class member modifiers such as `static`, `private`, and `protected` remain unsupported by the compiler.

## KAG implementation

[`internal/kag/parser.go`](../internal/kag/parser.go) parses source into ordered tokens/tags and restorable cursors. Runtime [`KAGParser`](../internal/runtime/kag_parser.go) exposes parser state; [`kag_execution.go`](../internal/runtime/kag_execution.go) adds bounded `Step`/`Run` execution and callbacks. The documented subset includes conditions, jump/call/return/end, ignore blocks, macros/parameter macros, `emb`, inline scripts, callbacks, budgets, and state restoration. Physical-line `[iscript]`/`[endscript]` blocks and their `@iscript`/`@endscript` forms preserve source and start-line metadata, run only on active branches through the Session VM's `onScript` callback, and report unterminated blocks at their source location. `KAGParserEx` shares the same implementation.
The native-compatible surface also exposes `clear`, `assign`, and `popMacroArgs`, plus read-only `mp`/`macroParams` snapshots for the active macro frame. Assignment copies parser state into the existing target object and keeps the target's VM-bound callbacks.

### Compatibility exercised by the lovelyday replay

The compatibility changes exercised this round cover KAG macro argument handling, structural line breaks and comments, and the pointer-to-script interaction chain. Macro argument frames expose the arguments expected by parameter macros and are popped on completion; structural newlines and comments are kept distinct from visible scenario text and tags. The replayed interaction proceeds from pointer hit testing through the layer event callback into KAG/TJS handlers. This is tested behavior for the exercised paths, not blanket support for all KAG macros, tags, or input semantics.

### Known KAG gaps

- Nested macro definitions remain unsupported; the local Rust reference uses one active recording slot and the C++ checkout contains no KAGParser implementation.
- Read-only `curLineStr` reports the last physical source line read, starting with the first line on load; labels and jumps leave it unchanged until the next line is read. Parsed scenarios carry optional source lines, checkpoints persist the current-line index, and new cross-scenario frames serialize their source lines. Legacy checkpoints remain loadable, but old local checkpoints omitted the current-line index, so it is inferred best-effort from the cursor; old cross-scenario frames omitted source lines and cannot recover exact physical text.
- Native `macros` and `paramMacros` projections remain unavailable because macro bodies and parameter definitions are internal token/attribute collections.
- Broader C++ KAG tag behavior remains unimplemented; parser support does not imply full runtime tag execution.

## C++ comparison

The acceptance comparison is C++ TJS/KAG behavior, not just matching Rust API spellings. Unsupported bytecode and scenario operations return explicit errors instead of silent approximations.

**C++ reference:** [Kirikiri Z source](https://github.com/krkrz/krkrz). The workspace migration reference is `/Users/eslzzyl/WorkSpace/Rust/krkrz_rs`.
