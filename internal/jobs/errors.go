package jobs

import "errors"

// ErrJobNotFound is returned when an operation references a non-existent job ID.
var ErrJobNotFound = errors.New("job not found")
