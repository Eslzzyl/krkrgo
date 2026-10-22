# Rules for the temp directory

Every Go source file in this directory and its subdirectories must start with:

```go
//go:build ignore
```

This marker keeps the project-root Go tests from compiling temporary diagnostic code.
