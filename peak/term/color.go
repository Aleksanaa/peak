package terminal

// ANSI color values
const (
	Black Color = iota
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	LightGrey
	DarkGrey
	LightRed
	LightGreen
	LightYellow
	LightBlue
	LightMagenta
	LightCyan
	White
)

// Default colors are potentially distinct to allow for special behavior.
// For example, a transparent background. Otherwise, the simple case is to
// map default colors to another color.
const (
	DefaultFG Color = 0xff800000 + iota
	DefaultBG
)

// Color maps to the ANSI colors [0, 16), the xterm colors [16, 256),
// and 24-bit truecolor.
type Color uint32

// RGB returns a truecolor value for the given red, green, and blue components.
func RGB(r, g, b uint8) Color {
	return Color(0x01000000 | uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}

// IsRGB returns true if the color is a 24-bit truecolor.
func (c Color) IsRGB() bool {
	return (c & 0xff000000) == 0x01000000
}

// RGBComponents returns the red, green, and blue components of a truecolor.
func (c Color) RGBComponents() (r, g, b uint8) {
	return uint8(c >> 16), uint8(c >> 8), uint8(c)
}

// ANSI returns true if Color is within [0, 16).
func (c Color) ANSI() bool {
	return (c < 16)
}

var ansiPalette = [16][3]uint8{
	{0x00, 0x00, 0x00},
	{0x80, 0x00, 0x00},
	{0x00, 0x80, 0x00},
	{0x80, 0x80, 0x00},
	{0x00, 0x00, 0x80},
	{0x80, 0x00, 0x80},
	{0x00, 0x80, 0x80},
	{0xc0, 0xc0, 0xc0},
	{0x80, 0x80, 0x80},
	{0xff, 0x00, 0x00},
	{0x00, 0xff, 0x00},
	{0xff, 0xff, 0x00},
	{0x00, 0x00, 0xff},
	{0xff, 0x00, 0xff},
	{0x00, 0xff, 0xff},
	{0xff, 0xff, 0xff},
}
