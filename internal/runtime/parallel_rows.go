package runtime

import (
	"runtime"
	"sync"
)

// parallelRows splits a row range across a bounded set of workers. Callers
// must confine all reads and writes to the rows passed to each callback.
func parallelRows(rows, minRows int, fn func(start, end int)) {
	if rows <= 0 {
		return
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	if rows < minRows || workers == 1 {
		fn(0, rows)
		return
	}
	chunk := (rows + workers - 1) / workers
	var wg sync.WaitGroup
	for start := 0; start < rows; start += chunk {
		end := start + chunk
		if end > rows {
			end = rows
		}
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			fn(start, end)
		}(start, end)
	}
	wg.Wait()
}
