package xdm

import (
	"bufio"
	"io"

	"github.com/knroy/go-xml/v2/internal/xmldecode"
)

// decodeReader decodes a UTF-16 entity to UTF-8 and drops a byte order mark.
// The detection lives in internal/xmldecode, shared with dtd's loader.
func decodeReader(br *bufio.Reader) (io.Reader, error) { return xmldecode.Reader(br) }
