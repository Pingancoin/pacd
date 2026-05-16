package wallet

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Pingancoin/pacd/internal/chaincfg"
)

func ParsePACAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("amount must not be negative")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	frac := "0"
	if len(parts) == 2 {
		if len(parts[1]) > 8 {
			return 0, fmt.Errorf("too many decimal places")
		}
		frac = parts[1] + strings.Repeat("0", 8-len(parts[1]))
	}
	fracAtoms, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, err
	}
	if whole > (1<<63-1)/chaincfg.Coin {
		return 0, fmt.Errorf("amount overflow")
	}
	return whole*chaincfg.Coin + fracAtoms, nil
}
