# TJS memory optimization report

## Status and scope

This document records a static review of the TJS memory model and memory-optimization opportunities. The collector workspace follow-up is implemented in `internal/tjs/gc.go`; the report does not claim a whole-application memory reduction without a dedicated heap measurement.

The review covers:

- `internal/tjs/value.go`;
- `internal/tjs/object.go`;
- `internal/tjs/classes.go`;
- `internal/tjs/vm.go`;
- `internal/tjs/gc.go`;
- `internal/tjs/bytecode_decode.go`;
- `internal/tjs/bytecode_exec.go`;
- `internal/tjs/compiler.go`;
- `internal/tjs/literal.go`;
- `internal/tjs/preprocessor.go`.

The main distinction is:

```text
retained memory       memory still reachable after work completes
transient allocation  temporary memory created during execution
GC workspace          temporary collector data
compiled metadata     source, bytecode, descriptors, and debug information
native memory         memory outside the Go heap or held by native services
```

Allocation count alone cannot identify the retained-memory problem. The project needs separate measurements for live objects, retained metadata, temporary allocation, GC workspace, and native memory before choosing a collector or object-layout redesign.

## Executive conclusions

1. **The global dispatch registry is the highest-priority static memory concern.** `classes.go` stores callable, class, instance-class, property, and program-class metadata in process-global maps. Static search found registration paths but no corresponding deletion paths. VM GC removes entries from `VM.meta` and `ObjectTable`, but the process-global registry can retain metadata and descriptor graphs beyond the lifetime of a VM.
2. **Each script object can have several layers of fixed overhead.** A VM object may occupy an `ObjectTable` entry, a `VM.meta` entry, an `Object` with a mutex and member map, category-specific metadata, and additional class or dispatch metadata.
3. **Member storage duplicates key information.** The map key is a UTF-16-derived Go string, while each member also retains a UTF-16 key slice. This supports lookup and deterministic enumeration, but increases small-object footprint.
4. **Public ownership guarantees cause internal copying.** UTF-16 and octet accessors, array accessors, literal accessors, descriptor cloning, and object enumeration return owned copies. These copies protect API ownership but can create transient allocation and peak-memory pressure.
5. **Frames, scopes, and calls are major transient-allocation candidates.** Register slices, local maps, captured-scope slices, argument slices, rest/spread arrays, and property-call state are created across ordinary execution paths.
6. **Decoded and compiled representations retain more metadata than a compact runtime form would require.** TJS2 decoding materializes several typed pools and per-object arrays. Source compilation retains tagged instructions, descriptors, literals, and source locations.
7. **GC algorithm changes should come after retained-memory ownership is clarified.** Reusable mark buffers, lower root overhead, and generational collection may help, but they cannot correct metadata held strongly by process-global registries.
8. **Interpreter scratch reuse is now limited to stable per-VM and per-session
   workspaces.** The VM reuses top-level register storage and expanded-call
   argument capacity; session/runtime paths reuse paint, resource-ID, and
   transition workspaces. Nested function frames and captured scopes retain
   their existing ownership behavior.

The strongest immediate investigation target is registry ownership and cleanup. The strongest architectural target is a split between public owning representations and private compact runtime representations.

## Current memory model

### `Value`

`Value` is a tagged value containing one of:

- void;
- object reference;
- UTF-16 string units;
- octet bytes;
- integer;
- real number.

The scalar fields are inline. String and octet values retain slice headers pointing to backing storage. Object values retain stable object and context IDs rather than Go object pointers.

Relevant ownership behavior:

- `StringValue` converts input text to UTF-16 storage;
- `StringUnits` constructs a value from UTF-16 units;
- `OctetValue` copies input bytes;
- `Value.UTF16()` returns a copied UTF-16 slice;
- `Value.Octets()` returns copied bytes;
- `ToOctet` copies an existing octet value;
- `OctetBufferValue` is the exception: it borrows a native callback buffer without copying and marks it mutable, so indexed assignment and `Octet.xor` update that buffer in place; `OctetValue` results and script literals stay immutable;
- string concatenation allocates a new UTF-16 result.

The copy behavior is part of the public safety model. Internal VM paths need separate read-only or transfer-oriented APIs before any copy is removed.

### `Object`

Each `Object` contains:

```go
mu          sync.RWMutex
ID          ObjectID
Category    ObjectCategory
members     map[string]member
arrayLength int64
valid       bool
```

`NewObject` allocates an object and creates a member map even when the object has no members. This gives predictable behavior and simple mutation, with a fixed cost for every object.

A member contains:

```go
type member struct {
    key   UTF16
    value Value
    flags uint32
    valid bool
}
```

The map key and `member.key` represent the same logical name in different storage forms. `Entries` also creates temporary member and property-entry slices, copies keys, and converts keys to Go strings.

### `ObjectTable`

`ObjectTable` maintains several maps:

- object ID to `*Object`;
- class instance ID to class name list;
- registered class information;
- instance ID to class ID.

Invalidated objects remain addressable until explicit removal. This is required by the lifetime model, so object-table retention cannot be changed through ordinary invalidation alone.

### `VM.meta`

The VM keeps one `vmObject` record per allocated object. The record can retain:

- native names;
- `Function`, `Class`, and `Property` descriptors;
- captured scopes and `with` values;
- array elements and method maps;
- class and superclass relations;
- finalizer information;
- lexical context and owners;
- bound methods and contexts.

This metadata is required by execution and GC tracing, but it duplicates identity management already present in `ObjectTable`.

### Process-global dispatch registry

`classes.go` defines a process-global registry with maps for:

```go
callable       map[ObjectID]*callableMeta
classes        map[ObjectID]*classMeta
objects        map[ObjectID]*classMeta
props          map[ObjectID]*propertyMeta
programClasses map[string]*classMeta
```

Registration occurs for functions, classes, instances, and property markers. The static source review found no deletion operations for these maps.

Potential retained structures include:

- `callableMeta.fn.Program`;
- cloned class definitions;
- method and property descriptors;
- class object pointers;
- class base graphs;
- bound values and contexts;
- instance-to-class mappings.

This needs a lifecycle decision before other memory work:

1. make the registry VM-local;
2. associate each entry with an owning VM and remove it during collection;
3. use explicit VM disposal for registry cleanup;
4. use weak references where the API permits it;
5. retain a process-global registry only for genuinely process-global native descriptors.

A cleanup design must account for public constructors such as `FunctionValue`, `NewClass(nil, ...)`, native functions, and cross-VM use. The registry cannot be made weak without first defining the lifetime of callable and class values.

## Memory categories and likely drivers

### Retained object overhead

Likely contributors:

- one `Object` allocation;
- one member map;
- one `vmObject` allocation;
- entries in `ObjectTable` and `VM.meta`;
- class and dispatch metadata;
- mutex and map bucket overhead;
- member key storage;
- array backing storage where applicable.

This category is most important for many small objects, functions, properties, class markers, and short-lived arrays.

### Temporary execution memory

Likely contributors:

- `VM.run` register slices;
- local scope maps;
- captured scope copies;
- call argument slices;
- rest and spread arrays;
- property getter/setter arguments;
- transient roots;
- instruction roots;
- temporary UTF-16 and octet copies;
- array and dictionary conversion helpers.

This category affects allocation rate, GC frequency, and peak working-set size.

### Compiled metadata

Likely contributors:

- token arrays;
- preprocessor source units and location arrays;
- source locations on every instruction;
- tagged `Op` fields;
- nested literal trees;
- cloned Function/Class/Property descriptors;
- decoded TJS2 pools;
- source position arrays;
- retained code and constant tables.

This category affects startup memory and long-lived script caches.

### GC workspace

The collector allocates or grows:

- the mark set;
- pending object IDs;
- host list;
- object ID list from `allIDs`;
- frame-root traversal state.

This category is short-lived, but it can create collection-time peaks proportional to the live object count.

## Highest-priority issue: registry lifecycle

### Static finding

The source has insertion and lookup paths for the process-global registry. A static search found no matching `delete` operation for the registry maps. `VM.collectGarbage` removes unreachable IDs from `ObjectTable`, `VM.meta`, and selected VM root maps, but it does not remove entries from the global registry.

### Why this matters

A function or class descriptor can retain a complete `Program` tree. A class metadata record can retain its object pointer and base metadata. A property marker can remain in `registry.props` after its VM object has been collected. Repeated VM creation or repeated script compilation can therefore retain metadata even when the owning VM no longer retains the corresponding object graph.

### Design options

#### VM-local registry

Move dispatch metadata into `VM` or an owner object.

Advantages:

- clear lifetime;
- collection can inspect all metadata;
- no cross-VM contamination;
- simpler memory accounting.

Costs:

- public constructors currently accept an optional `ObjectTable` rather than a VM;
- some APIs create values without an explicit VM;
- dispatch code needs an owner lookup path.

#### Explicit owner and cleanup

Keep a registry abstraction but attach an owner token to every entry. Remove entries when the owner disposes the VM or when GC proves the object unreachable.

Advantages:

- smaller API change;
- supports current registration model.

Costs:

- cleanup must handle class bases, callables, property markers, and bound contexts;
- explicit disposal can be missed;
- GC-triggered registry mutation requires careful locking.

#### Process-global native registry plus VM-local script registry

Keep immutable native descriptors global. Keep script functions, classes, properties, and instances VM-local.

This is the most natural ownership split if native methods are genuinely shared and script values are VM-owned.

### Required semantic review

Before changing registry ownership, inspect:

- `FunctionValue` callers;
- `NewClass` callers with nil tables;
- native function and method registration;
- class metadata lookup from `Program.Storage`;
- cross-VM tests and host integrations;
- finalizer and invalidation paths.

No cleanup design should be selected from memory size alone.

## Object and member storage

### Key duplication

Ordinary members store both a map key and a UTF-16 member key. The duplicated key supports fast lookup and deterministic UTF-16 enumeration, but it increases memory for every member.

Candidate designs:

1. shared immutable key objects;
2. VM-local name interning;
3. shape-level shared names for class instances;
4. numeric name IDs in member records;
5. dictionary-specific key storage for highly dynamic objects.

A key table must have a lifetime limit. A global intern table can retain every dynamic name ever observed. VM-local or bounded tables have predictable retention.

### Small-object representation

Many objects have few members. A possible representation is:

```text
zero members       → no map allocation
small fixed count  → inline member array
stable class       → shared shape plus value slots
dynamic dictionary → current map representation
```

This needs to preserve:

- enumeration order;
- UTF-16 key behavior;
- flags;
- invalidated members;
- property descriptors;
- deletion;
- class and superclass lookup.

A dictionary promotion path is preferable to forcing all object categories into one layout.

### Shape and slots

Stable class instances could share a shape:

```go
type Shape struct {
    ID       uint32
    Parent   *Shape
    Slots    map[string]uint32
}
```

Instances would retain a shape pointer and a value-slot array. The shape would own shared member names and slot metadata.

This can reduce both key duplication and map overhead for stable instances. It requires transition handling for member insertion, deletion, accessors, flags, and dynamic class behavior.

## Strings, octets, and ownership

### Internal read-only access

Public accessors should retain copies where callers receive data they may mutate. Internal code can use methods that expose read-only slices under a clear lifetime contract:

```text
public Value.UTF16()  → owned copy
internal valueUnits() → borrowed read-only slice
public Value.Octets() → owned copy
internal octetBytes() → borrowed read-only slice
```

The internal methods must not escape slices into mutable or long-lived structures.

### Literal and constant sharing

Repeated script literals can share immutable storage through:

- one VM-local constant pool;
- shared `UTF16` backing data;
- interned member names;
- immutable octet constants;
- references from prepared instructions.

Literal mutation must still create a runtime object or copy. Constant sharing applies to immutable compiled data.

### String concatenation

`Value.Binary("+")` creates a new UTF-16 result for string concatenation. Repeated concatenation in loops can create many intermediate strings. Possible later work includes:

- compiler recognition of simple concatenation chains;
- builder-backed specialized bytecode;
- capacity-aware concatenation;
- direct formatting paths for interpolation.

These optimizations need to preserve UTF-16 code-unit semantics and observable evaluation order.

## Frames, scopes, and calls

### Current allocation sources

`VM.run` allocates a register slice per frame. Function invocation can copy:

- captured scope slices;
- captured `with` values;
- argument slices;
- local scope maps.

Rest and spread paths allocate arrays or expanded argument slices. Property invocation allocates additional call state.

### Slot-based lexical locals

Functions without `with`, dynamic declaration behavior, or relevant `eval` can use fixed lexical slots. A slot frame can replace a map for ordinary local reads and writes.

Dynamic functions retain a map-backed path. The compiler can mark a function as:

```text
static lexical scope
closure scope
with-enabled scope
eval-sensitive scope
```

The classification must be conservative.

### Capture analysis

A closure should retain only variables that escape into that closure. Candidate representation:

```text
non-escaping local → frame slot
captured local     → shared environment slot
dynamic name       → map-backed scope
```

The environment must remain visible to GC and support nested closures, recursive functions, native re-entry, and exception unwinding.

### Frame reuse

A frame arena can reduce register and scope allocation for non-reentrant calls. Reuse requires:

- a depth-aware lifetime stack;
- explicit cleanup on panic/error paths;
- root publication before native calls;
- support for nested `Execute`;
- closure escape detection;
- no reuse while a value remains referenced by a heap object.

Frame reuse is a later optimization after retained registry and object costs are understood.

## Arrays and collection storage

### Current representation

VM arrays store a `[]Value` plus a logical length. This is efficient for dense arrays and supports holes through void entries, but a large logical length or sparse index can reserve a substantial contiguous slice.

Additional copies occur through:

- `NewArray`, which copies input items;
- `ArrayItems`, which returns a copy;
- rest and spread handling;
- array-to-dictionary conversion;
- serialization and assignment helpers.

### Candidate representations

```text
packed array       → []Value
sparse array       → index map or packed-prefix plus sparse tail
numeric array      → specialized integer/real storage
large temporary    → capacity-aware reusable buffer
```

A numeric representation needs promotion when a value of another kind, a hole, or an accessor is introduced. Sparse storage needs careful handling of logical length, enumeration, deletion, and array methods.

### Capacity policy

Potential policies include:

- capacity shrink after a large reduction in logical length;
- retain capacity for arrays known to be reused;
- release capacity on explicit `clear`;
- compact sparse arrays after deletion density crosses a threshold.

Each policy trades allocation churn against retained capacity.

## Compiled and decoded metadata

### TJS2 decoded data

`DecodeBytecode` materializes independent arrays for typed constant pools, strings, octets, objects, source positions, code, constants, superclass getters, and properties. It also copies byte and octet payloads from the input container.

Potential design:

```text
validated input container
    ↓
immutable shared pools
    ↓
prepared instruction views
    ↓
optional debug/source metadata
```

A zero-copy representation may reduce memory but requires retaining the original bytecode container and enforcing immutable input ownership. Copying remains appropriate when the caller may reuse or mutate the input buffer.

### Legacy preparation cache

Caching decoded instructions and offset maps can improve repeated execution, but it also retains extra memory. The cache should be bounded or attached to the decoded program lifetime. It should not create a second full representation when a compact view can reference existing code and constant pools.

### Source compiler data

The source path may retain:

- preprocessed UTF-16 units;
- source location arrays;
- token arrays;
- literal trees;
- tagged instructions;
- nested descriptors;
- debug source locations.

A runtime-only compilation mode can release tokens and preprocessing data after `Program` creation. A compact prepared form can separate execution data from source maps.

## GC memory and root storage

### Collector workspace

`collectGarbage` now retains VM-local buffers for the mark map, pending
object IDs, host roots, and sorted object IDs. The collector still preserves
host roots, frame registers, legacy registers, transient roots, captured
scopes, class relations, finalizers, native outgoing references, and
deterministic ID ordering.

Implemented workspace reuse:

- reusable mark map;
- reusable pending-ID buffer;
- reusable host buffer;
- reusable sorted-ID buffer.

Remaining review candidates:

- an object-table iterator;
- maintained active-object vectors;
- compact root ranges instead of repeated root slices.

Any change must retain host roots, frame registers, legacy registers, transient roots, captured scopes, class relations, finalizers, and native outgoing references.

### Generational collection

A nursery and old generation could reduce scanning for workloads with many short-lived arrays and temporary objects. It requires:

- write barriers or remembered sets;
- generation-aware root scanning;
- promotion policy;
- finalizer handling;
- host-reference treatment;
- class and registry ownership rules.

Generational GC should follow lifetime data. It is not the first design response to the current static findings.

## Memory diagnostics

The VM should eventually expose diagnostic counters for:

- live object count by category;
- live member count;
- member-key bytes;
- UTF-16 bytes;
- octet bytes;
- array length and capacity;
- frame and scope bytes;
- compiled program bytes;
- decoded TJS2 bytes;
- registry entries and retained descriptor bytes;
- GC workspace peak;
- bytes reclaimed per collection;
- allocation count by source path;
- peak resident memory.

Counters should distinguish:

```text
allocated bytes
live bytes
retained bytes after collection
peak bytes
```

A diagnostic snapshot should identify the owner category of retained data. A total heap number alone cannot distinguish a registry retention problem from a temporary allocation problem.

## Priority and risk matrix

| Priority | Area | Static assessment | Benefit | Main risk |
| --- | --- | --- | ---: | --- |
| P0 | Global registry lifecycle | Registration paths have no visible deletion paths | very high | API and ownership changes |
| P1 | Object fixed overhead | ObjectTable, VM metadata, Object, and registry can overlap | high | representation complexity |
| P1 | Member key sharing | map key and UTF-16 member key coexist | medium to high | enumeration and mutation semantics |
| P1 | Internal slice copies | Several public and VM paths copy values and arrays | medium | ownership violations |
| P2 | Frame and scope slots | Registers, maps, captures, and args are repeatedly allocated | high | closures, re-entry, exceptions |
| P2 | Compiled metadata split | Source and runtime data coexist | medium to high | source mapping and cache invalidation |
| P2 | Legacy preparation cache | Repeated preparation is possible | startup and call benefit | retained cache memory |
| P3 | Array specialization | Dense and sparse patterns share one backing model | workload-dependent | promotion and method semantics |
| P3 | GC workspace reuse | VM-local mark, pending, host, and sorted-ID buffers are reused | medium | collector state management |
| P4 | Generational GC | May help short-lived object workloads | workload-dependent | barriers and root complexity |

## Recommended investigation sequence

1. Define registry ownership and inspect every API that creates callable or class metadata.
2. Add a static memory inventory by object category and retained metadata owner.
3. Separate retained memory from transient allocation in the diagnostic design.
4. Review internal copy paths and introduce private read-only access patterns where ownership permits.
5. Design compact prepared bytecode and source-map separation.
6. Evaluate slot-based locals and captured environments.
7. Evaluate member-name sharing and shape/slot storage for stable instances.
8. Evaluate array representations from workload distributions.
9. GC workspace reuse is implemented with VM-local mark, pending, host, and sorted-ID buffers.
10. Consider generational collection only after object lifetime data supports it.

## Correctness contract

Memory changes must preserve:

- object identity and stable object IDs;
- invalidated-object addressability;
- UTF-16 code-unit behavior;
- member enumeration order;
- member flags and accessor behavior;
- array holes and logical length;
- closure capture and receiver context;
- native re-entry;
- nested execution;
- exception and finally unwinding;
- finalizers;
- host-provided GC roots;
- serialization behavior;
- deterministic replay behavior where applicable.

The key design principle is ownership clarity: every retained descriptor, object, backing slice, cache entry, and registry record needs an explicit owner and lifetime.
