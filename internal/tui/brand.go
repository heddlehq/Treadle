package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// The logos are woven: the coral weft passes over and under the cream warps, and
// the one-pixel gaps show which is on top. '#' is warp, 'o' is weft.
var (
	treadleMark = []string{
		"...........##...........",
		"..........####..........",
		"..........####..........",
		".oooooooo.####.oooooooo.",
		"ooooooooo.####.ooooooooo",
		"ooooooooo.####.ooooooooo",
		".oooooooo.####.oooooooo.",
		"..........####..........",
		"..........####..........",
		"..........####..........",
		"..........####..........",
		"..........####..........",
		"..........####..........",
		"..........####..........",
		"..........####..........",
		"...........##...........",
	}
	heddleMark = []string{
		".....##........##......",
		"....####......####.....",
		"....####......####.....",
		"....####......####.....",
		"....####......####.....",
		"....####......####.....",
		"..............####.....",
		".oooooooooooo.####.ooo.",
		"ooooooooooooo.####.oooo",
		"ooooooooooooo.####.oooo",
		".oooooooooooo.####.ooo.",
		"..............####.....",
		"....####......####.....",
		"....####......####.....",
		"....####......####.....",
		"....####......####.....",
		"....####......####.....",
		".....##........##......",
	}
	treadleMarkSmall = []string{
		".....##.....",
		"oooo.##.oooo",
		"oooo.##.oooo",
		".....##.....",
		".....##.....",
		".....##.....",
	}
	heddleMarkSmall = []string{
		"..##...##..",
		"..##...##..",
		".......##..",
		"oooooo.##.o",
		"oooooo.##.o",
		".......##..",
		"..##...##..",
		"..##...##..",
	}
)

// brandLockup joins the two marks with a thread at the height of their wefts.
type brandLockup struct {
	treadle     []string
	heddle      []string
	treadleTop  int
	threadTop   int
	threadRows  int
	threadWidth int
}

var (
	fullLockup = brandLockup{
		treadle:     treadleMark,
		heddle:      heddleMark,
		treadleTop:  4,
		threadTop:   8,
		threadRows:  2,
		threadWidth: 12,
	}
	smallLockup = brandLockup{
		treadle:     treadleMarkSmall,
		heddle:      heddleMarkSmall,
		treadleTop:  2,
		threadTop:   3,
		threadRows:  2,
		threadWidth: 8,
	}
)

type threadState int

const (
	threadIdle threadState = iota
	threadConnecting
	threadFailed
)

const threadPulseWidth = 4

func (l brandLockup) width() int {
	return len(l.treadle[0]) + l.threadWidth + len(l.heddle[0])
}

func (l brandLockup) pixelHeight() int {
	height := max(l.treadleTop+len(l.treadle), len(l.heddle))
	return height + height%2
}

// rows returns the height of the lockup in terminal rows.
func (l brandLockup) rows() int {
	return l.pixelHeight() / 2
}

func (l brandLockup) render(state threadState, frame int) []string {
	pixels := make([][]color.Color, l.pixelHeight())
	for y := range pixels {
		pixels[y] = make([]color.Color, l.width())
	}

	paintMark(pixels, l.treadle, 0, l.treadleTop)
	paintMark(pixels, l.heddle, len(l.treadle[0])+l.threadWidth, 0)

	for x := range l.threadWidth {
		thread := threadColor(x, l.threadWidth, state, frame)
		for y := l.threadTop; y < l.threadTop+l.threadRows; y++ {
			pixels[y][len(l.treadle[0])+x] = thread
		}
	}

	return renderPixels(pixels)
}

// renderMark draws a single logo with no thread.
func renderMark(mark []string) []string {
	pixels := make([][]color.Color, len(mark))
	for y := range pixels {
		pixels[y] = make([]color.Color, len(mark[0]))
	}
	paintMark(pixels, mark, 0, 0)
	return renderPixels(pixels)
}

func paintMark(pixels [][]color.Color, mark []string, left, top int) {
	for y, row := range mark {
		for x, pixel := range row {
			switch pixel {
			case '#':
				pixels[top+y][left+x] = textColor
			case 'o':
				pixels[top+y][left+x] = accentColor
			}
		}
	}
}

func threadColor(x, width int, state threadState, frame int) color.Color {
	switch state {
	case threadFailed:
		if x >= width/2-1 && x <= width/2 {
			return nil
		}
		return errorColor
	case threadConnecting:
		head := frame % (width + threadPulseWidth)
		if x <= head && x > head-threadPulseWidth {
			return accentColor
		}
		return threadDimColor
	default:
		if x%3 == 2 {
			return nil
		}
		return threadDimColor
	}
}

// renderPixels draws two pixels per cell, so the art stays square in a terminal.
func renderPixels(pixels [][]color.Color) []string {
	rows := make([]string, 0, (len(pixels)+1)/2)
	for y := 0; y < len(pixels); y += 2 {
		var b strings.Builder
		for x := range pixels[y] {
			var bottom color.Color
			if y+1 < len(pixels) {
				bottom = pixels[y+1][x]
			}
			b.WriteString(renderPixelPair(pixels[y][x], bottom))
		}
		rows = append(rows, b.String())
	}
	return rows
}

func renderPixelPair(top, bottom color.Color) string {
	switch {
	case top == nil && bottom == nil:
		return " "
	case top == nil:
		return lipgloss.NewStyle().Foreground(bottom).Render("▄")
	case bottom == nil:
		return lipgloss.NewStyle().Foreground(top).Render("▀")
	case top == bottom:
		return lipgloss.NewStyle().Foreground(top).Render("█")
	default:
		return lipgloss.NewStyle().Foreground(top).Background(bottom).Render("▀")
	}
}

// renderBrandTitle is the one-row mark used in headers. The weft runs along the
// top of the row, high on the warp as in the logo, and the warp sits in the middle
// of its cell, which leaves the gaps of the weave.
func renderBrandTitle() string {
	weft := lipgloss.NewStyle().Foreground(accentColor).Render("▔▔")
	warp := lipgloss.NewStyle().Foreground(textColor).Render("┃")
	name := lipgloss.NewStyle().Foreground(textColor).Bold(true).Render("Treadle")
	return weft + warp + weft + " " + name
}
