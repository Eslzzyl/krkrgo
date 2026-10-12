# TJS compiler and interpreter optimization report

## Scope and semantic target

The official C++ implementation at `/Users/eslzzyl/WorkSpace/krkrz/tjs2` is the semantic authority for TJS behavior; Rust is a migration reference only. Go optimizations must preserve observable language semantics, exception/finally and scope unwinding, source locations, execution budgets, tracing, and garbage-collector rooting. The six production changes below are narrow helper, key-conversion, and call-path optimizations. Measurements are local Darwin/arm64 results, not evidence of a full-VM or whole-application speedup.

## C++ and Go execution models

C++ declares wordcode opcodes and specialized property/call forms in `tjs2/tjsInterCodeGen.h:33-78`; `tjs2/tjsInterCodeGen.cpp:590-638,855-900,1044-1053` emits code, collapses jump chains, and compacts the code area. Its execution loop is `tTJSInterCodeContext::ExecuteCode` in `tjs2/tjsInterCodeExec.cpp:1019-1470`.

Go uses string `OpCode`s and a tagged `Op` in `internal/tjs/compiler.go:18-124`; `(*VM).run` in `internal/tjs/vm.go:1340-1477` validates and executes `[]Instruction`, maintaining roots, safe points, budgets, execution counts, source traces, and optional host tracing. The broad property path is in `internal/tjs/vm.go:1582-1705`; call dispatch and invocation are in `internal/tjs/vm.go:1788-1807,2082-2128`. A future private execution representation must retain the serializable program contract and these observable behaviors.

## Completed production results

All six optimizations were implemented and verified on Darwin arm64. Relevant tests and benchmarks are identified below; the full verification commands are listed afterward.

1. **Direct scalar UTF-16 equality** — `internal/tjs/value.go:641-650` compares code units directly rather than calling `compareUTF16`. Equality and ordering boundary coverage includes empty strings, prefixes, and isolated surrogates (`internal/tjs/value_object_test.go:28-57`). On Apple M1 Pro, equal 32-unit inputs measured 12.69–13.65 ns/op versus 16.55–16.72 ns/op for the compare-based baseline; 1,024 units measured 346.0–351.1 ns/op versus 502.8–513.3 ns/op; 65,536 units measured 21.16–21.38 µs/op versus 31.79–32.19 µs/op. Both paths used zero allocations. These are helper benchmarks, not a VM speedup claim.

2. **Direct UTF-16-unit member keys** — `memberKeyUnits` and the ordinary `getMember`, `setMember`, and `deleteMember` paths in `internal/tjs/vm.go:307-319,357-458` use existing units with `Object.GetUnits`, `SetUnits`, and `DeleteUnits`; conversion to text is deferred for global/class/super/dictionary fallback. Coverage includes non-ASCII and isolated-surrogate keys, misses, fallback, accessors, and dynamic add/delete (`internal/tjs/vm_member_access_test.go:5-90`). The ordinary-hit microbenchmark improved from 129.9 ns/op, 32 B/op, 2 allocs/op to 60.78 ns/op, zero bytes and allocations. This is not a property cache or opcode specialization.

3. **Direct Go-string property-name keys** — `memberKeyString` in `internal/tjs/object.go` converts Go names directly to exact little-endian UTF-16 member keys; `internal/tjs/vm.go` routes `loadName`/`storeName` through `getMemberString`/`setMemberString`. Existing UTF-16-unit paths preserve Value-key semantics, including isolated surrogates. `internal/tjs/vm_member_access_test.go` and `internal/tjs/vm_member_benchmark_test.go` cover access and performance. `BenchmarkVMPropertyAccess` measured 6,429–6,496 ns/op, 14,534–14,536 B/op, 49 allocs/op versus the earlier 6,650–6,688 ns/op, 14,620–14,622 B/op, 55 allocs/op. Getter measured 2,868–2,881 ns/op, 6,063–6,064 B/op, 23 allocs/op; setter 4,314–4,432 ns/op, 10,213–10,214 B/op, 32 allocs/op. This ordinary name path still allocates and is not an inline cache.

4. **Removed `UTF16ToString` defensive copy** — `internal/tjs/utf16.go:44` now uses `UTF16(units).String()` rather than copying through `NewUTF16`, with no unsafe code. `internal/tjs/utf16_conversion_test.go` covers ASCII, NUL, BMP, emoji, isolated surrogates, and long inputs. Before/after wrapper benchmarks reduced allocations from two to one for representative short strings (for example, short ASCII 72 to 24 B/op; Japanese 40 to 24 B/op; emoji 32 to 16 B/op). The later direct standard-library decode comparison was within timing noise with identical allocations; the established result is removal of the redundant copy, not a universal timing gain.

5. **Shared `VM.handle` error sentinel** — `internal/tjs/vm.go:70,234-242` reuses `errTJSNonNullObject` for non-object/null values instead of constructing a fixed error each call. Error text and `VMAbortError` wrapping remain unchanged; `internal/tjs/vm_exception_test.go:96-114` and `internal/tjs/delete_test.go:73-81` cover error behavior. On Apple M1 Pro, non-object/null handling fell from about 10 ns/op, 16 B/op, one allocation to about 2.5–2.6 ns/op, zero bytes and allocations. This is a localized error-path result.

6. **Owned `expandArgs` slice for script calls** — `OpCall` passes its expanded argument slice through `invokeOwned`/`invokeWithOwnership` (`internal/tjs/vm.go:1810-1819,2104-2154`); script callees use it as frame arguments rather than making a second append copy. Public invocation and native/method/construction ownership boundaries retain their behavior. Call-shape tests and benchmarks are in `internal/tjs/vm_test.go:770-784` and `internal/tjs/vm_call_benchmark_test.go:16-86`. Paired medians: script/fixed 3,458→3,402 ns/op, 8,508→8,330 B/op, 26→25 allocs/op; script/forward 5,390→5,295 ns/op, 13,179→12,827 B/op, 42→40; script/spread 3,504→3,451 ns/op, 7,965→7,787 B/op, 28→27. These are local call-path results only.

### Evaluated candidates and property-frame comparison

The benchmark-only SIMD UTF-16 prototype was slower than scalar for every measured equal and last-different input size on Apple M1 Pro, with zero allocations in both paths. `github.com/tphakala/simd` accelerated an unrelated `i16.Sum` reduction for which this TJS code has no corresponding helper; `github.com/kelindar/simd` contains C code-generation sources, contrary to the literal no-C-source constraint, and provides no relevant UTF-16 comparator. Neither dependency was added. Generic dynamic TJS opcode dispatch is heterogeneous and sequential; these results do not justify SIMD production code.

The property-frame review made no production change. Go `invokePropertyFunction` creates a lexical-scope slice and map (`internal/tjs/vm.go:920-940`), setter calls create an input slice (`internal/tjs/vm.go:2519`), and `run` allocates registers (`internal/tjs/vm.go:1393`). By comparison, C++ `ExecuteAsFunction` obtains contiguous `TJSVariantArrayStack` storage (`tjs2/tjsInterCodeExec.cpp:776-940`); getter/setter reserve counts are set in `tjs2/tjsInterCodeGen.cpp:262-263`, with pointer/register argument transfer. This is a structural comparison, not a measured end-to-end comparison. No stable production frame-allocation reduction was demonstrated. Any arena or compact/lazy scope design must preserve GC visibility, closures, parameters, receiver/context, reentrancy, budgets, tracing, source behavior, and exception/finally behavior.

### Verification

The reported post-change checks passed:

- `go test ./internal/tjs -count=1`
- `go test ./... -count=1`
- `go build ./...`
- `go vet ./...`

These checks establish the reported test/build status, not overall VM or application performance.

## Remaining research candidates

These are open research directions, not pending production optimizations:

- **Private predecode or compact opcode representation:** Go's string opcodes and tagged instruction are in `internal/tjs/compiler.go:18-124`; benchmark a private execution form without changing the public/serializable program contract or losing validation, source maps, budgets, tracing, and GC visibility.
- **Peephole and jump-chain pass:** C++ jump-chain handling is in `tjs2/tjsInterCodeGen.cpp:855-900`; Go branch patching is in `internal/tjs/compiler.go:1402`. Any pass needs correct target remapping and preservation of observable instruction accounting, source locations, and unwind behavior.
- **Guarded property fast path:** profile repeated access before considering guards around `OpGet`/`OpSet` (`internal/tjs/vm.go:1582-1754`) and object storage (`internal/tjs/object.go:101-120`). Mutation, prototypes, accessors, and dynamic receiver/key patterns constrain safe caching.
- **Frame arena or lazy scope representation:** the Go/C++ allocation comparison above motivates research only; correctness constraints include GC roots, closure capture, reentrancy, and exceptions.
- **JIT and GPU:** deferred, non-general paths. JIT would require runtime code generation, GC/root maps, unwinding, tracing, budgets, and portability; ordinary dynamic TJS execution is a stateful sequential stream, not a general GPU workload. Reconsider only with measured workload evidence and an explicit runtime/support case.
