//go:build race

package chaos_test

// raceDetectorEnabled reports whether this test binary was built with -race.
// The soak calibrates its heap tolerance for a non-race run; the race
// detector's 5–8x memory overhead makes post-GC heap numbers meaningless, so
// the heap assertion is dropped under -race and the goroutine assertion kept
// (S4.T10). The `race` build tag is set automatically by `go test -race`.
const raceDetectorEnabled = true
