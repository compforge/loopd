package runtime

import (
	"github.com/compforge/loopd/runtime/model"
)

// Error is shared by every runtime layer without importing the root facade.
type Error = model.Error

var ErrCallConflict = model.ErrCallConflict

func IsConflict(err error) bool { return model.IsConflict(err) }

// IsHarnessCapacityExceeded reports admission rejection; no new Run was accepted.
func IsHarnessCapacityExceeded(err error) bool { return model.IsHarnessCapacityExceeded(err) }
func IsRetryable(err error) bool               { return model.IsRetryable(err) }
