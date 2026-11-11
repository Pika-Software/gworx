package fsutil

import (
	"fmt"
	"os"
	"time"
)

// SetAsideDamaged renames a file that could not be parsed to
// "<path>.bad-<timestamp>", so that saving fresh data doesn't destroy it.
// It returns an error describing what happened, wrapping cause.
func SetAsideDamaged(path string, cause error) error {
	backup := fmt.Sprintf("%s.bad-%s", path, time.Now().Format("20060102-150405"))
	if err := os.Rename(path, backup); err != nil {
		return fmt.Errorf("%s is damaged (%w) and could not be moved aside: %v", path, cause, err)
	}
	return fmt.Errorf("%s was damaged (%w); it was moved to %s and a new one will be created", path, cause, backup)
}
