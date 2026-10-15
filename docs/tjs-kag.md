# TJS and KAG Porting Status

## Reference and scope

The Rust tree is a migration/API reference; official C++ [`krkrz`](https://github.com/krkrz/krkrz) is the semantic and acceptance target. TJS implementation is in `internal/tjs`; KAG tokenization is in `internal/kag`, with execution/native integration in `internal/runtime`.

## TJS implementation

[`compiler.go`](../internal/tjs/compiler.go) provides scanning, parsing, preprocessing, and compilation through `Compile`, `CompileExpression`, and `CompileEval`; eval-operator scripts accept either expressions or statements. [`vm.go`](../internal/tjs/vm.go) implements `VM`, execution, object/class and native dispatch, and budget-aware execution. Related source includes [`object.go`](../internal/tjs/object.go), [`dispatch.go`](../internal/tjs/dispatch.go), [`gc.go`](../internal/tjs/gc.go), and [`preprocessor.go`](../internal/tjs/preprocessor.go).

The implemented TJS subset includes functions, closures, rest/spread/forward arguments, exception handling, `with`, loop/switch control flow, octet literals, native/class dispatch, conditional preprocessing, bounded arrays, program validation, and serialization. In a call, `...` forwards all arguments received by the current function, while `*` forwards only its unnamed-rest arguments; array spread remains available as `array*`. `with` member bindings participate in name lookup for declarations and assignments as well as reads; a declaration initializer updates a matching active `with` member before introducing a local, while an uninitialized declaration neither writes nor masks an existing member. It does not represent full TJS2 compatibility.
VM host array helpers `NewArray`, `AppendArray`, and `ClearArray` keep array elements and length visible through both VM dispatch and the legacy object-table dispatcher.

When a function runs with an `incontextof` receiver, assignments to declared script globals update the existing global member, so subsequent unqualified reads see the assigned value rather than a stale `void` member.

TJS arrays support in-place `sort(orderOrComparator, stable)`: `+`/omitted, `-`, `0`, `9`, `a`, and `z` select normal, reverse, numeric, reverse-numeric, string, and reverse-string order; an object comparator receives `(left, right)` and returns a truthy value when the left item precedes the right. The optional second argument enables stable sorting, and `sort` returns `void`.

Top-level named function declarations are available from the start of their script, including when referenced by earlier statements; their original declaration statement is retained so declaration-time replacement behavior remains intact. Function declarations nested in blocks or `with` contexts are not hoisted.

Script class declarations compile to the existing `Class`, `Function`, and `Property` descriptors. The VM supports empty classes, multiple evaluated base expressions, base-to-derived instance initialization, class-named constructors, inherited method/property lookup, `super` method/property dispatch, native-class initialization through the class dispatch path, rebinding cached native base-constructor calls to the instance under construction, and default parameter expressions in functions and class methods.

An `&object.member` property reference binds before `incontextof`, so `*(&object.member incontextof receiver)` dereferences the descriptor on `receiver`; assigning through the reference invokes its setter on that same object. Unbound references retain the execution context used by the dereference.

Legacy `SETP` writes through VM-owned property descriptors using the bound receiver. `invalidate` runs a script instance's finalizer before clearing its valid state.

The Go scanner joins adjacent string literals that use the same quote delimiter, including across whitespace and newlines. Comments and differing delimiters keep literals separate; commas in arrays and call arguments and the ordinary `+` operator remain parser-level syntax.

The TJS preprocessor supports `@if`, `@elif`, `@else`, and `@endif` conditional directives in source before compilation. These are TJS preprocessor directives, not KAG scenario tags.

TJS String values expose `sprintf` using the C++ `TJSFormatString` subset: integer (`diouxX`), real (`fegEG`), and string (`cs`) conversions, flags, fixed or `*` width/precision, and escaped percent signs. `escape` provides the C-style escaping used by script-generated expressions. `split` treats its first argument as a set of UTF-16 delimiter characters and its third, optional argument purges empty fields. String member lookup exposes supported methods only; an unknown property such as `storage` is not manufactured into a method. Formatting and string precision follow UTF-16 code-unit semantics.
TJS `Math` is provided by the runtime native with constants and the implemented standard numeric operations. The package-level xorshift `random` generator and its seeding behavior are described in [Runtime and native services](runtime-natives.md).



### Runtime preprocessor environment

The official C++ runtime initializes the TJS preprocessor symbol `kirikiriz` to `1`, and the Rust runtime follows the same convention. Go runtime sessions now initialize the same symbol and reuse one preprocessor across `Session.ExecuteStorage` and `Scripts` storage execution, preserving definitions across source units.

### Known TJS gaps

- Session storage execution supports source TJS and the implemented compiled `TJS2` intercode subset, including `TYPEOF` (in-place conversion to the TJS type name), class-object constants, class-body initialization, deferred superclass getters, member methods, and VM construction. Unsupported bytecode instructions still return explicit errors.
- Registered `Scripts.compileStorage`, `Scripts.dump`, and object-valued `Scripts.setCallMissing` operations return unsupported errors: bytecode output, heap dumping, and missing-member dispatch are unavailable.
- `Scripts.textEncoding` applies to `Scripts` storage execution; `Session.ExecuteStorage` retains its separate automatic-decoding behavior.
- Class member modifiers such as `static`, `private`, and `protected` remain unsupported by the compiler.

## KAG implementation

[`internal/kag/parser.go`](../internal/kag/parser.go) parses source into ordered tokens/tags and restorable cursors. Runtime [`KAGParser`](../internal/runtime/kag_parser.go) exposes parser state; [`kag_execution.go`](../internal/runtime/kag_execution.go) adds bounded `Step`/`Run` execution and callbacks. The documented subset includes conditions, jump/call/return/end, ignore blocks, macros/parameter macros, `emb`, inline scripts, callbacks, budgets, and state restoration. Physical-line `[iscript]`/`[endscript]` blocks and their `@iscript`/`@endscript` forms preserve source and start-line metadata, run only on active branches through the Session VM's `onScript` callback, and report unterminated blocks at their source location. `KAGParserEx` shares the same implementation.
The native-compatible surface also exposes `clear`, `assign`, and `popMacroArgs`, plus read-only `mp`/`macroParams` snapshots for the active macro frame. Assignment copies parser state into the existing target object and keeps the target's VM-bound callbacks.

Line-command tags written as `@tag` are recognized after leading spaces or tabs, as in the tab-indented `@clip`/`@wait` commands in Amairo animation scenarios. Indented ordinary text remains text.

A lone `*` line is ignored rather than indexed as an empty first label; named labels such as `*first` are still indexed normally.

An empty label passed to `Jump`/`goToLabel` is a no-op, matching C++ KAG behavior. This permits storage-only transitions to load a scenario without attempting to resolve an empty label.

`KAGParser.callLabel` keeps its return frame independently of the active scenario cursor, so callers may load another storage and jump to its target before returning to the original storage.

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
