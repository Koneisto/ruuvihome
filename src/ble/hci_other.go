//go:build !linux

package ble

import (
	"context"
	"fmt"
)

// runHCISession: the direct HCI scanner needs Linux (AF_BLUETOOTH user channel).
func (s *Scanner) runHCISession(ctx context.Context) error {
	return fmt.Errorf("bluetooth.mode hci is only supported on Linux")
}
