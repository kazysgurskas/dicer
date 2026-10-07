// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"fmt"
	"strconv"
)

// enum pairs the values of one of the client's enumerations with the API's.
type enum[T ~string, P ~int32] struct {
	// what names the enumeration, as an error does: "hypervisor type".
	what   string
	values map[T]P
}

// toProto returns the API's value for v: unspecified for the empty string,
// and an error wrapping ErrInvalidArgument for a value the API does not
// have.
func (e enum[T, P]) toProto(v T) (P, error) {
	if v == "" {
		return 0, nil
	}
	p, ok := e.values[v]
	if !ok {
		return 0, fmt.Errorf("%w: unknown %s %q", ErrInvalidArgument, e.what, v)
	}

	return p, nil
}

// fromProto returns the client's value for p: the empty string for
// unspecified, and the number itself for a value this client is too old to
// know.
func (e enum[T, P]) fromProto(p P) T {
	if p == 0 {
		return ""
	}
	for v, candidate := range e.values {
		if candidate == p {
			return v
		}
	}

	return T(strconv.Itoa(int(p)))
}
