package main

import "testing"

// Text in a format peak knows is read as UTF-8 with LF line ends, and
// written back byte for byte; anything else is read as it is.
func TestDecodeTextRoundTrip(t *testing.T) {
	for _, tt := range []struct{ name, in, text string }{
		{"plain", "a\nb\n", "a\nb\n"},
		{"crlf", "a\r\nb\r\n", "a\nb\n"},
		{"mixed line ends", "a\r\nb\n", "a\r\nb\n"},
		{"lone cr", "a\rb", "a\rb"},
		{"utf-8 bom", "\xef\xbb\xbfhi\r\n", "hi\n"},
		{"utf-16le", "\xff\xfeh\x00i\x00\r\x00\n\x00", "hi\n"},
		{"utf-16be", "\xfe\xff\x00h\x00i", "hi"},
		{"empty", "", ""},
	} {
		text, f, err := decodeText([]byte(tt.in))
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if text != tt.text {
			t.Errorf("%s: text = %q, want %q", tt.name, text, tt.text)
		}
		if back := f.encode(text); string(back) != tt.in {
			t.Errorf("%s: written back as %q, want %q", tt.name, back, tt.in)
		}
	}
}

// A NUL makes a file binary, and so does UTF-16 that would not decode
// back to itself.
func TestDecodeTextBinary(t *testing.T) {
	for _, in := range []string{"a\x00b", "\xff\xfe\x00\xd8a\x00"} {
		if _, _, err := decodeText([]byte(in)); err != errBinary {
			t.Errorf("%q: err = %v, want errBinary", in, err)
		}
	}
}

// Text typed into a window is written in its file's format.
func TestEncodeEditedText(t *testing.T) {
	_, f, _ := decodeText([]byte("\xff\xfea\x00\r\x00\n\x00"))
	got := f.encode("a\nb\n")
	if want := "\xff\xfea\x00\r\x00\n\x00b\x00\r\x00\n\x00"; string(got) != want {
		t.Errorf("encode = %q, want %q", got, want)
	}
}
