package main

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

// A textFormat is how a file stores its text when that is not plain UTF-8
// with LF line ends: after a byte order mark, in UTF-16, with CRLF line
// ends. Get notes it and Put restores it, so a file is written back as it
// was read. The zero textFormat is plain.
type textFormat struct {
	bom  *byteOrderMark // the mark the file starts with, if any
	crlf bool           // every line ends in CRLF
}

// A byteOrderMark starts a file whose text is in UTF-16 of byte order
// order, or in UTF-8 if order is nil.
type byteOrderMark struct {
	mark  string
	order binary.ByteOrder
}

var boms = []byteOrderMark{
	{"\xef\xbb\xbf", nil},
	{"\xff\xfe", binary.LittleEndian},
	{"\xfe\xff", binary.BigEndian},
}

// decodeText returns the text of data, in UTF-8 with LF line ends, and its
// decodeText returns the text of data, in UTF-8 with LF line ends, and its
// format. A format is taken only if writing the text back in it gives data
// again; anything else is read as it is. Text with a NUL in its first 512
// bytes is binary.
func decodeText(data []byte) (string, textFormat, error) {
	var f textFormat
	text := data
	for i, b := range boms {
		if bytes.HasPrefix(data, []byte(b.mark)) {
			f.bom = &boms[i]
			text = data[len(b.mark):]
			if b.order != nil {
				u := make([]uint16, len(text)/2)
				for i := range u {
					u[i] = b.order.Uint16(text[2*i:])
				}
				text = []byte(string(utf16.Decode(u)))
			}
			break
		}
	}
	if n := bytes.Count(text, []byte("\n")); n > 0 && bytes.Count(text, []byte("\r\n")) == n {
		f.crlf = true
		text = bytes.ReplaceAll(text, []byte("\r\n"), []byte("\n"))
	}
	if f != (textFormat{}) && !bytes.Equal(f.encode(string(text)), data) {
		f, text = textFormat{}, data
	}
	if bytes.IndexByte(text[:min(len(text), 512)], 0) >= 0 {
		return "", textFormat{}, errBinary
	}
	return string(text), f, nil
}

// encode returns text as stored in format f.
func (f textFormat) encode(text string) []byte {
	b := []byte(text)
	if f.crlf {
		b = bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
	}
	if f.bom == nil {
		return b
	}
	if f.bom.order != nil {
		u := utf16.Encode([]rune(string(b)))
		b = make([]byte, 2*len(u))
		for i, c := range u {
			f.bom.order.PutUint16(b[2*i:], c)
		}
	}
	return append([]byte(f.bom.mark), b...)
}
