//go:build !race

package chaos_test

// raceDetectorEnabled reports whether this test binary was built with -race.
// False here, so the soak applies both the goroutine and the post-GC heap
// assertion (S4.T10).
const raceDetectorEnabled = false
