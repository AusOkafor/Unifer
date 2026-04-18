package merge

import "errors"

// ErrAlreadyMerged is returned when the duplicate group is already merged
// (e.g. concurrent merge completed first).
var ErrAlreadyMerged = errors.New("already_merged")
