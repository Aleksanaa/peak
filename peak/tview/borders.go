package tview

const (
	BoxDrawingsLightHorizontal              rune = '\u2500'
	BoxDrawingsLightVertical                rune = '\u2502'
	BoxDrawingsLightDownAndRight            rune = '\u250c'
	BoxDrawingsLightDownAndLeft             rune = '\u2510'
	BoxDrawingsLightUpAndRight              rune = '\u2514'
	BoxDrawingsLightUpAndLeft               rune = '\u2518'
	BoxDrawingsDoubleHorizontal             rune = '\u2550'
	BoxDrawingsDoubleVertical               rune = '\u2551'
	BoxDrawingsDoubleDownAndRight           rune = '\u2554'
	BoxDrawingsDoubleDownAndLeft            rune = '\u2557'
	BoxDrawingsDoubleUpAndRight             rune = '\u255a'
	BoxDrawingsDoubleUpAndLeft              rune = '\u255d'

	SemigraphicsHorizontalEllipsis rune = '\u2026'
)

var Borders = struct {
	Horizontal, Vertical                   rune
	TopLeft, TopRight, BottomLeft, BottomRight rune
	HorizontalFocus, VerticalFocus         rune
	TopLeftFocus, TopRightFocus            rune
	BottomLeftFocus, BottomRightFocus      rune
}{
	Horizontal:  BoxDrawingsLightHorizontal,
	Vertical:    BoxDrawingsLightVertical,
	TopLeft:     BoxDrawingsLightDownAndRight,
	TopRight:    BoxDrawingsLightDownAndLeft,
	BottomLeft:  BoxDrawingsLightUpAndRight,
	BottomRight: BoxDrawingsLightUpAndLeft,

	HorizontalFocus:  BoxDrawingsDoubleHorizontal,
	VerticalFocus:    BoxDrawingsDoubleVertical,
	TopLeftFocus:     BoxDrawingsDoubleDownAndRight,
	TopRightFocus:    BoxDrawingsDoubleDownAndLeft,
	BottomLeftFocus:  BoxDrawingsDoubleUpAndRight,
	BottomRightFocus: BoxDrawingsDoubleUpAndLeft,
}
