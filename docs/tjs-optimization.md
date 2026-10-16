# TJS compiler and interpreter optimization report

## Scope and semantic target

The official C++ implementation at `/Users/eslzzyl/WorkSpace/krkrz/tjs2` is the semantic authority for TJS behavior; Rust is a migration reference only. Go optimizations must preserve observable language semantics, exception/finally and scope unwinding, source locations, execution budgets, tracing, and garbage-collector rooting. The seven production changes below include narrow helper, key-conversion, call-path, and Legacy preparation optimizations. Measurements include local microbenchmarks and a Windows amd64 `testdata/amairo` replay comparison.

## C++ and Go execution models

C++ declares wordcode opcodes and specialized property/call forms in `tjs2/tjsInterCodeGen.h:33-78`; `tjs2/tjsInterCodeGen.cpp:590-638,855-900,1044-1053` emits code, collapses jump chains, and compacts the code area. Its execution loop is `tTJSInterCodeContext::ExecuteCode` in `tjs2/tjsInterCodeExec.cpp:1019-1470`.

Go uses string `OpCode`s and a tagged `Op` in `internal/tjs/compiler.go:18-124`; `(*VM).run` in `internal/tjs/vm.go:1340-1477` validates and executes `[]Instruction`, maintaining roots, safe points, budgets, execution counts, source traces, and optional host tracing. The broad property path is in `internal/tjs/vm.go:1582-1705`; call dispatch and invocation are in `internal/tjs/vm.go:1788-1807,2082-2128`. A future private execution representation must retain the serializable program contract and these observable behaviors.

## Completed production results

All seven optimizations were implemented and verified. Relevant tests and benchmarks are identified below; the full verification commands are listed afterward.

1. **Direct scalar UTF-16 equality** — `internal/tjs/value.go:641-650` compares code units directly rather than calling `compareUTF16`. Equality and ordering boundary coverage includes empty strings, prefixes, and isolated surrogates (`internal/tjs/value_object_test.go:28-57`). On Apple M1 Pro, equal 32-unit inputs measured 12.69–13.65 ns/op versus 16.55–16.72 ns/op for the compare-based baseline; 1,024 units measured 346.0–351.1 ns/op versus 502.8–513.3 ns/op; 65,536 units measured 21.16–21.38 µs/op versus 31.79–32.19 µs/op. Both paths used zero allocations. These are helper benchmarks, not a VM speedup claim.

2. **Direct UTF-16-unit member keys** — `memberKeyUnits` and the ordinary `getMember`, `setMember`, and `deleteMember` paths in `internal/tjs/vm.go:307-319,357-458` use existing units with `Object.GetUnits`, `SetUnits`, and `DeleteUnits`; conversion to text is deferred for global/class/super/dictionary fallback. Coverage includes non-ASCII and isolated-surrogate keys, misses, fallback, accessors, and dynamic add/delete (`internal/tjs/vm_member_access_test.go:5-90`). The ordinary-hit microbenchmark improved from 129.9 ns/op, 32 B/op, 2 allocs/op to 60.78 ns/op, zero bytes and allocations. This is not a property cache or opcode specialization.

3. **Direct Go-string property-name keys** — `memberKeyString` in `internal/tjs/object.go` converts Go names directly to exact little-endian UTF-16 member keys; `internal/tjs/vm.go` routes `loadName`/`storeName` through `getMemberString`/`setMemberString`. Existing UTF-16-unit paths preserve Value-key semantics, including isolated surrogates. `internal/tjs/vm_member_access_test.go` and `internal/tjs/vm_member_benchmark_test.go` cover access and performance. `BenchmarkVMPropertyAccess` measured 6,429–6,496 ns/op, 14,534–14,536 B/op, 49 allocs/op versus the earlier 6,650–6,688 ns/op, 14,620–14,622 B/op, 55 allocs/op. Getter measured 2,868–2,881 ns/op, 6,063–6,064 B/op, 23 allocs/op; setter 4,314–4,432 ns/op, 10,213–10,214 B/op, 32 allocs/op. This ordinary name path still allocates and is not an inline cache.

4. **Removed `UTF16ToString` defensive copy** — `internal/tjs/utf16.go:44` now uses `UTF16(units).String()` rather than copying through `NewUTF16`, with no unsafe code. `internal/tjs/utf16_conversion_test.go` covers ASCII, NUL, BMP, emoji, isolated surrogates, and long inputs. Before/after wrapper benchmarks reduced allocations from two to one for representative short strings (for example, short ASCII 72 to 24 B/op; Japanese 40 to 24 B/op; emoji 32 to 16 B/op). The later direct standard-library decode comparison was within timing noise with identical allocations; the established result is removal of the redundant copy, not a universal timing gain.

5. **Shared `VM.handle` error sentinel** — `internal/tjs/vm.go:70,234-242` reuses `errTJSNonNullObject` for non-object/null values instead of constructing a fixed error each call. Error text and `VMAbortError` wrapping remain unchanged; `internal/tjs/vm_exception_test.go:96-114` and `internal/tjs/delete_test.go:73-81` cover error behavior. On Apple M1 Pro, non-object/null handling fell from about 10 ns/op, 16 B/op, one allocation to about 2.5–2.6 ns/op, zero bytes and allocations. This is a localized error-path result.

6. **Owned `expandArgs` slice for script calls** — `OpCall` passes its expanded argument slice through `invokeOwned`/`invokeWithOwnership` (`internal/tjs/vm.go:1810-1819,2104-2154`); script callees use it as frame arguments rather than making a second append copy. Public invocation and native/method/construction ownership boundaries retain their behavior. Call-shape tests and benchmarks are in `internal/tjs/vm_test.go:770-784` and `internal/tjs/vm_call_benchmark_test.go:16-86`. Paired medians: script/fixed 3,458→3,402 ns/op, 8,508→8,330 B/op, 26→25 allocs/op; script/forward 5,390→5,295 ns/op, 13,179→12,827 B/op, 42→40; script/spread 3,504→3,451 ns/op, 7,965→7,787 B/op, 28→27. These are local call-path results only.
7. **Cached Legacy preparation** — `internal/tjs/bytecode_decode.go` allocates one preparation record per decoded object, while `internal/tjs/legacy_intercode.go` caches instruction boundaries and offset-to-IP tables through `sync.Once`. `ExecuteLegacy...` reuses the prepared representation; public `DecodeLegacyInstructions` returns an owned copy. On Windows amd64, the full `testdata/amairo` replay changed from 15.535 s to 13.191 s for the 1 s checkpoint and from 126.510 s to 61.061 s for the 10 s checkpoint. Replay JSON SHA-256 values remained identical.

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

- **Private execution form (skipped candidate):** a compact Legacy instruction representation without the public `Name` field was measured against the cached `Legacy` path. The `testdata/amairo` 10 s replay changed from 61.061 s to 63.111 s with identical output SHA-256, so the candidate was withdrawn.
- **Persistent preparation cache (skipped candidate):** the current process-local cache removes repeated preparation work within one decoded program. A cross-start cache needs a versioned serialized format and invalidation policy; the current replay path does not provide a stable measurement that separates disk-cache cost from the operating-system file cache.
- **Peephole and jump-chain pass:** C++ jump-chain handling is in `tjs2/tjsInterCodeGen.cpp:855-900`; Go branch patching is in `internal/tjs/compiler.go:1402`. Any pass needs correct target remapping and preservation of observable instruction accounting, source locations, and unwind behavior.
- **Guarded property fast path:** profile repeated access before considering guards around `OpGet`/`OpSet` (`internal/tjs/vm.go:1582-1754`) and object storage (`internal/tjs/object.go:101-120`). Mutation, prototypes, accessors, and dynamic receiver/key patterns constrain safe caching.
- **Frame arena or lazy scope representation:** the Go/C++ allocation comparison above motivates research only; correctness constraints include GC roots, closure capture, reentrancy, and exceptions.
- **JIT and GPU:** deferred, non-general paths. JIT would require runtime code generation, GC/root maps, unwinding, tracing, budgets, and portability; ordinary dynamic TJS execution is a stateful sequential stream, not a general GPU workload. Reconsider only with measured workload evidence and an explicit runtime/support case.
 
## Optimization architecture assessment

The current implementation has two execution paths: the source compiler emits a serializable register program consumed by `(*VM).run`, while decoded TJS2 intercode is interpreted through `ExecuteLegacy...`. Neither path currently has a JIT or native-code AOT tier. The most valuable next steps are interpreter and metadata improvements that preserve the existing semantic contract.

### Recommended execution tiers

1. **Cached preparation (implemented):** retain decoded Legacy instructions, offset-to-IP tables, compact constant metadata, and call-site metadata on the decoded program instead of rebuilding instruction metadata for every invocation.
2. **Private execution form:** lower `Program` into integer opcodes and compact operands while retaining the current serializable/debug representation as the public form. The private form must preserve validation, source locations, budgets, tracing, exception/finally unwinding, and GC roots.
3. **Interpreter fast paths:** specialize integer arithmetic, local register moves, direct jumps, and common load/store operations. Trace and diagnostic checks can use a separate execution mode so ordinary execution pays less fixed cost.
4. **Feedback-guided caches:** add guarded caches for repeated member reads, writes, and calls. A cache needs an object-layout/version guard and a generic fallback for mutation, accessors, dynamic receivers, `with`, `eval`, and class lookup.
5. **Specialized bytecode:** after hotness data is available, produce specialized versions for stable call sites and numeric loops. This tier can provide JIT-like benefits without introducing native code generation.

### Compiler and object-model opportunities

The compiler currently allocates registers monotonically. Constant folding, copy propagation, jump-chain cleanup, basic-block metadata, and live-range-based register reuse are natural compiler passes. Fixed local names can use slots in functions that do not require dynamic `with` lookup; closures and dynamic scope retain the existing map-based path.

Object member lookup currently combines UTF-16 key handling, object-table lookup, locking, map access, VM metadata, and class/property fallback. A future shape-and-slot layout, combined with guarded inline caches, could make stable instance members direct slot accesses. The dynamic map path remains necessary for dictionary-like objects and structural mutation.

### JIT and AOT assessment

A persistent optimized bytecode cache remains the practical AOT direction: compile source once, predecode Legacy intercode, build instruction metadata, and reload the prepared form on later starts. The runtime now has an in-memory per-decoded-program Legacy preparation cache; persistent cross-start storage remains a later step. Native-code AOT or JIT requires platform-specific code generation, executable-memory policy, Go ABI integration, precise GC root maps, exception unwinding, tracing, budgets, and deoptimization back to the interpreter.

The recommended overall sequence is:

```text
predecode cache → compact interpreter → compiler passes → guarded inline caches
→ specialized bytecode → native JIT only for measured hot functions
```

These directions are research guidance. They are not claims of whole-application speedup and do not replace workload-specific profiling.
 
## Comprehensive optimization report

### Executive summary

The highest-value path is a tiered runtime. The first tier now exists for decoded Legacy programs: instruction boundaries and offset-to-IP tables are prepared once per object and reused by later calls. The next tiers remain private execution representation, interpreter fast paths, guarded member/call caches, and specialized bytecode.

The highest-value path is a tiered runtime:

```text
source / TJS2
    ↓
validated prepared representation
    ↓
compact interpreter
    ↓ hot functions and call sites
specialized bytecode + guarded caches
    ↓ measured high-value functions
optional native JIT
```

Native JIT should remain an optional late tier. The first tiers can address instruction decoding, object lookup, call overhead, allocation pressure, and startup cost while preserving the existing Go runtime contract.

### Scope and non-goals

This report covers:

- `internal/tjs/compiler.go`;
- `internal/tjs/vm.go`;
- `internal/tjs/bytecode_decode.go`;
- `internal/tjs/bytecode_exec.go`;
- `internal/tjs/object.go`;
- `internal/tjs/dispatch.go`;
- `internal/tjs/gc.go`;
- TJS benchmarks and runtime startup behavior.

It covers interpreter, compiler, object-layout, caching, AOT, JIT, GC, measurement, and rollout concerns.

It does not propose changing TJS language semantics, removing `eval`, weakening execution budgets, changing exception behavior, or requiring every script to use native code.

### Existing execution model

#### Source compiler

The source compiler emits `Program`, a serializable register program containing `[]Instruction`. The instruction representation uses a string opcode and one tagged operand structure. Registers are allocated monotonically by `compiler.reg()`. The compiler performs semantic lowering, control-flow patching, function hoisting, and validation, but it does not currently expose a distinct optimization IR or a liveness-based register allocator.

#### Source interpreter

`(*VM).run` validates the program, allocates a register slice, maintains frame roots, checks the execution budget, increments the instruction counter, optionally invokes host tracing, and dispatches through a large opcode switch. The same loop handles ordinary arithmetic, dynamic name lookup, object access, calls, constructors, exceptions, `with`, `eval`, and class behavior.

#### TJS2 intercode

`DecodeBytecode` validates and decodes TJS2100 records. `ExecuteLegacy...` decodes instruction boundaries, builds an offset-to-instruction map, allocates the register file, resolves operands, and executes the legacy instruction set. Repeated calls can therefore repeat preparation work unless the decoded representation retains prepared metadata.

#### Object and value model

`Value` is a tagged value containing TJS integers, reals, UTF-16 strings, octets, or stable object references. Objects have stable IDs, a member map, category-specific VM metadata, and synchronization around member storage. VM lookup combines object resolution, UTF-16 key handling, member-map lookup, class fallback, properties, native methods, and receiver binding.

#### Memory management

The VM uses explicit roots and mark-sweep collection. Roots include globals, explicit roots, literals, transient call roots, interpreter registers, frames, legacy registers, and host-provided references. Automatic collection occurs at interpreter safe points after an allocation threshold. This root model is a strong foundation for an optimized interpreter and a future JIT, provided compiled frames publish equivalent root information.

### Workload model

Optimization decisions should use separate workload classes because each stresses a different subsystem:

| Workload | Dominant paths | Primary metrics |
| --- | --- | --- |
| Startup script | compile, decode, VM setup, native calls, allocation | time to first scene, compile time, first execution |
| Numeric loop | opcode dispatch, `Value` arithmetic, branches | ns/instruction, instructions/iteration |
| Local-variable function | frame creation, scopes, registers, calls | ns/call, allocations/call |
| Member loop | UTF-16 keys, object map, class fallback | ns/get, ns/set, cache hit rate |
| Method loop | member lookup, receiver binding, invocation | ns/call, allocations/call |
| Array loop | array indexing, holes, length handling | ns/access, allocations |
| Property loop | getter/setter invocation and scopes | ns/access, allocations |
| Closure workload | captured scopes, lifetime, GC roots | allocation rate, collection time |
| KAG event script | VM/native transitions, short-lived programs | event latency, startup overhead |
| Legacy TJS2 workload | predecode and intercode dispatch | decode time, execution time |

The project already has isolated VM call, member, trace, UTF-16, and run-phase benchmarks. Those should be supplemented with representative scripts from `testdata` and a small set of repeatable startup traces.

### Measurement protocol

Every optimization should report:

- wall-clock time;
- CPU time where available;
- allocations per operation;
- bytes allocated per operation;
- instruction count;
- instructions per second;
- GC count and collection time;
- cache hit/miss counts;
- first-run and repeated-run behavior;
- semantic test status.

Measurements should separate:

1. compilation;
2. one-time preparation;
3. first execution;
4. repeated execution;
5. steady-state execution;
6. garbage collection;
7. native calls.

The benchmark harness should use fixed source, fixed input, a large enough budget, disabled tracing unless tracing is the subject, and a stable VM state. Results should record OS, architecture, Go version, compiler flags, and benchmark commit.

### Cost model

The current likely costs are:

1. per-instruction interpreter dispatch and loop bookkeeping;
2. dynamic name and member lookup;
3. function frame and argument slice creation;
4. map and UTF-16 key work;
5. VM-to-native and native-to-VM transitions;
6. temporary object and slice allocation;
7. periodic full GC;
8. TJS2 instruction preparation on repeated calls;
9. source compilation during startup;
10. trace and budget checks in hot loops.

These are hypotheses to validate with profiles and counters. They should not be treated as a fixed ranking for every game.

## Tier 0: preparation and cache

### TJS2 predecode cache

Store prepared Legacy metadata on `DecodedBytecode`:

- decoded instructions;
- offset-to-IP mapping;
- operand classifications;
- branch targets;
- constant classifications;
- call-site descriptors;
- source-location references;
- maximum register requirements.

The cache must be immutable after publication or protected for concurrent readers. `ExecuteLegacy...` should reuse it and preserve the current validation errors.

### Source-program preparation

Add a private prepared form derived from `Program`:

- integer opcode;
- compact operands;
- prevalidated register references;
- direct branch indexes;
- interned names and member keys;
- instruction flags;
- basic-block indexes;
- call-site indexes.

Keep `Program` as the serializable/debug contract. Prepared data can be ephemeral or persisted separately.

### Persistent AOT cache

A practical AOT cache can store optimized bytecode and metadata rather than native machine code. Its key should include:

- source or compiled-bytecode content hash;
- compiler version;
- TJS semantic version;
- platform-independent format version;
- relevant preprocessor state;
- feature flags affecting code generation.

The loader must validate lengths, counts, references, version fields, and checksums before use. A stale or invalid cache should compile from the original source or decoded bytecode.

## Tier 1: compact interpreter

### Integer opcodes

Use an internal `uint16` or `uint32` opcode while retaining names for diagnostics. A compact instruction should separate common register operands from uncommon metadata. This reduces instruction size and makes dispatch more predictable.

### Dispatch choices

Candidate implementations, from conservative to aggressive:

1. integer `switch`;
2. grouped opcode handlers;
3. direct handler table;
4. computed-goto style dispatch where the target platform and Go toolchain justify it;
5. a specialized loop for a small set of arithmetic and register instructions.

The first option is the preferred starting point because it keeps stack traces, budgets, and debugging straightforward.

### Fast and slow paths

Fast paths should cover:

- integer arithmetic and comparison;
- register move;
- direct jump;
- local slot load/store;
- constant load;
- known array index;
- cached member access;
- cached script/native call.

Slow paths should cover:

- `eval`;
- `with`;
- dynamic `delete`;
- property access;
- class resolution;
- receiver rebinding;
- exceptions;
- invalid or unusual types;
- host callbacks.

The slow path must retain identical observable behavior.

### Instruction accounting

Budget accounting and `VM.Executed` are observable through errors and diagnostics. Any grouped or superinstruction design must define whether one fused unit consumes one budget unit or the original instruction count. The safer compatibility choice is to retain logical instruction accounting even when physical dispatch is fused.

## Tier 2: compiler optimization

### Safe local transformations

The first compiler passes should include:

- constant folding for side-effect-free primitive expressions;
- copy propagation;
- redundant move removal;
- jump-chain cleanup;
- unreachable block removal;
- basic-block metadata;
- live-range-based register reuse;
- direct constant-name encoding.

Each pass must preserve source locations used in errors and tracing.

### Register allocation

Monotonic register allocation increases register-file size and root scanning work. A linear-scan allocator can reuse registers after their last use. Values that cross calls, exception handlers, closures, or `with` operations require conservative liveness treatment.

The allocator must retain:

- values live across native calls;
- values referenced by pending finally handlers;
- closure-captured values;
- receiver and lexical context;
- values needed by source-level tracing;
- GC-visible object references.

### Local slots and dynamic scope

Functions with ordinary lexical locals can use fixed slots. Functions containing `with`, dynamic declaration behavior, or relevant `eval` should retain a map-backed scope path. This creates a fast lexical mode and a dynamic-scope mode without changing language behavior.

### Superinstructions

Common sequences can be fused:

```text
load local → constant → integer add → store local
```

```text
load object → load constant key → get member
```

Fused instructions must retain source mapping and logical budget accounting.

## Tier 3: object layout and inline caches

### Shape-and-slot layout

Dictionary-like objects can retain map storage. Stable class instances can use a shape plus value slots:

```go
type Shape struct {
    ID       uint32
    Parent   *Shape
    Slots    map[string]uint32
}
```

An object shape changes when member layout changes. Shape transitions should be immutable and shareable. `delete`, dynamic member insertion, accessors, and unusual flags can transition to a dictionary representation.

### Inline-cache forms

Use a cache per member or call site:

- uninitialized;
- monomorphic;
- small polymorphic;
- megamorphic/generic.

A cache entry can contain:

- receiver shape or category;
- member key;
- slot or member ID;
- callable category;
- receiver-binding rule;
- property/accessor metadata.

### Invalidation

Invalidation must account for:

- shape changes;
- member deletion;
- accessor installation;
- member flags;
- class-base changes;
- native method replacement;
- `eval` or dynamic declaration affecting lookup;
- global and `with` scope changes.

An epoch counter is simpler than trying to enumerate every dependent cache. Each relevant mutation increments the epoch; entries compare their recorded epoch before using cached data.

### Call-site caching

For `obj.method(args)`, a combined cache can skip separate lookup and receiver binding. Script function, native function, method, property, and class constructor calls need distinct cache actions.

## Tier 4: feedback and specialized bytecode

### Runtime feedback

Collect compact counters at:

- function entry;
- loop back edges;
- member reads and writes;
- call sites;
- type checks;
- cache misses;
- exception paths.

Counters should be disabled or sampled in low-overhead production mode. They can be enabled through a diagnostic configuration for profiling runs.

### Specialization candidates

Generate specialized bytecode for:

- integer-only arithmetic loops;
- stable member slots;
- direct script calls;
- direct native calls;
- fixed-arity calls;
- arrays with packed numeric indexes;
- functions without dynamic scope.

Each specialized operation needs a guard and a generic fallback. A guard failure should switch the call site to generic execution or trigger deoptimization.

### On-stack replacement

OSR can replace a hot loop while it is running. It requires a mapping from interpreter registers and scope state to specialized-frame slots. This is useful for long-running numeric or animation loops, but it requires more machinery than function-entry specialization.

## Tier 5: native JIT

### Feasibility

Native JIT is technically possible, but the current runtime needs an explicit machine-code ABI. The generated code would need stable access to:

- `Value`;
- object IDs;
- VM state;
- budget state;
- exception state;
- frame roots;
- host/native calls.

### Required runtime services

A production JIT needs:

- executable-memory allocation;
- instruction-cache synchronization on arm64;
- platform-specific calling conventions;
- code lifetime management;
- safepoints;
- stack maps;
- GC root maps;
- exception and finally unwinding;
- source-location mapping;
- tracing hooks;
- budget checks;
- deoptimization;
- invalidation of stale compiled code.

### Deoptimization

Optimized code should retain enough metadata to reconstruct:

- interpreter instruction pointer;
- register values;
- lexical scopes;
- receiver and owner;
- pending exception/finally state;
- source location.

Without this mapping, dynamic features such as `eval`, property calls, class dispatch, and exceptions cannot safely return to the interpreter.

### Implementation choices

Possible implementation families include:

1. a small platform-specific assembler backend;
2. a portable machine-code assembler library;
3. LLVM or Cranelift integration;
4. WebAssembly as a portable compiled tier;
5. a specialized bytecode tier with no native machine code.

The specialized-bytecode option has the lowest integration cost. LLVM or Cranelift provides stronger code generation but adds toolchain, binary-size, and deployment complexity. WebAssembly gives a portable sandboxed target but still requires a value ABI, host-call bridge, GC integration, and deoptimization strategy.

## GC and allocation strategy

### Immediate opportunities

Profile and reduce:

- argument slice copies;
- closure scope copies;
- register-frame allocation;
- temporary UTF-16 key buffers;
- temporary arrays from spread/rest;
- repeated Legacy instruction slices;
- map creation for short-lived scopes;
- full object-ID sorting during GC.

### Frame reuse

A frame arena or reusable register storage can reduce allocations. It must handle nested execution, reentrancy, host callbacks, exceptions, and closures. Reuse is valid only when all live values remain visible to the GC root system.

### Generational collection

A nursery/old-generation design may help workloads with many temporary arrays and intermediate objects. It requires write barriers or an equivalent remembered-set mechanism. This should follow allocation profiles, because the existing explicit mark-sweep collector is simpler and already integrated with host roots.
 

## Native-call and host-boundary optimization

Native calls are a major semantic boundary. Candidate improvements include:

- stable native function IDs instead of repeated string dispatch;
- pre-resolved native call descriptors;
- fixed-arity argument paths;
- direct `Value` argument access for common native methods;
- reduced temporary slices for zero- and one-argument calls;
- explicit result-needed flags;
- batching only for APIs whose semantics permit it.

Native calls that can re-enter the VM must retain all current transient roots and budget behavior.

## Correctness contract

Every optimization must preserve:

- TJS value conversion rules;
- UTF-16 code-unit behavior;
- object identity and bound contexts;
- property getter/setter order;
- evaluation order and side effects;
- `with` lookup;
- `eval`;
- exception and finally unwinding;
- `delete`;
- class and superclass resolution;
- native re-entry;
- source locations;
- instruction budgets;
- host tracing;
- GC roots;
- deterministic replay behavior where applicable.

The optimization test suite should emphasize observable transitions and semantic cases rather than internal implementation details.

## Risk matrix

| Area | Expected benefit | Main risk | Suggested entry point |
| --- | ---: | --- | --- |
| Legacy predecode | high startup and call benefit | stale metadata | immutable prepared cache |
| Compact opcode | high interpreter benefit | debug/source mapping drift | private execution form |
| Compiler passes | medium to high | side-effect or jump errors | constant folding and copy propagation |
| Local slots | high in ordinary functions | closure and `with` interaction | functions without dynamic scope |
| Inline caches | high member/call benefit | invalidation errors | monomorphic read cache |
| Shape layout | high stable-object benefit | object API complexity | class instances only |
| Specialized bytecode | high hot-loop benefit | guard/deopt complexity | function-entry specialization |
| Frame reuse | medium allocation benefit | GC root omissions | isolated benchmark first |
| Generational GC | workload-dependent | collector complexity | allocation-profile gate |
| Native JIT | potentially very high | ABI, GC, portability | prototype after hot-path proof |

## Recommended decision gates

### Gate A: preparation cost

Proceed with prepared caches if repeated execution spends a measurable fraction of time decoding, validating, or constructing instruction metadata.

### Gate B: interpreter cost

Proceed with compact opcodes and fast paths if instruction dispatch and loop bookkeeping dominate CPU profiles after preparation is cached.

### Gate C: dynamic lookup cost

Proceed with inline caches if member and call sites dominate representative script profiles and show stable receiver shapes or callable targets.

### Gate D: allocation cost

Proceed with slots, frame reuse, or collector changes if allocation and GC profiles dominate after dispatch and lookup improvements.

### Gate E: native code

Proceed with a JIT only if representative long-running functions remain CPU-dominant after the earlier tiers and the project accepts platform-specific runtime code generation.

## External design references

The following primary references provide useful design patterns:

- V8 hidden classes/maps: <https://v8.dev/docs/hidden-classes>
- V8 fast properties: <https://v8.dev/blog/fast-properties>
- V8 code caching: <https://v8.dev/blog/improved-code-caching>
- Oracle HotSpot compilation optimization: <https://docs.oracle.com/en/java/javase/11/jrockit-hotspot/compilation-optimization.html>
- Oracle HotSpot performance enhancements: <https://docs.oracle.com/en/java/javase/17/vm/java-hotspot-virtual-machine-performance-enhancements.html>
- Bun runtime and its JavaScriptCore foundation: <https://bun.sh/docs/runtime>
- Bun benchmarking guidance: <https://bun.sh/docs/project/benchmarking>

These systems validate recurring patterns: tiered execution, runtime feedback, stable object layouts, guarded specialization, code caching, and deoptimization. Their implementations are substantially larger than this runtime, so the applicable ideas should be introduced incrementally.

## Final recommendation

The project should treat native JIT as the final optimization tier, not the starting point. The strongest near-term architecture is:

```text
prepared metadata
→ compact interpreter
→ low-risk compiler passes
→ local slots
→ guarded member/call caches
→ specialized bytecode
→ optional native JIT
```

This sequence improves startup, steady-state execution, allocation behavior, and future JIT readiness while retaining one semantic execution model.
