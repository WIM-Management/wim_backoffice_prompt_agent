package state

import "strconv"

// EncodeByteCursor encodes an int64 byte offset as a decimal []byte cursor.
func EncodeByteCursor(off int64) []byte {
	return strconv.AppendInt(nil, off, 10)
}

// DecodeByteCursor decodes a decimal []byte cursor back to int64.
// If cur is nil/empty or unparseable, legacyOffset is returned as a safe fallback.
func DecodeByteCursor(cur []byte, legacyOffset int64) int64 {
	if len(cur) == 0 {
		return legacyOffset
	}
	v, err := strconv.ParseInt(string(cur), 10, 64)
	if err != nil {
		return legacyOffset
	}
	return v
}
