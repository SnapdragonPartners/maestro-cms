package pdf

import "context"

// LayoutVersion is the current value of Layout.Version. It changes whenever the
// serialized (JSON) form of Layout changes, so stored layouts say which form
// they use.
const LayoutVersion = 1

// BoxCrop is the Layout.Box value meaning coordinates are relative to each
// page's crop box, the region viewers display.
const BoxCrop = "crop"

// LayoutEngine is an optional interface for engines that can report the words
// on each page with their positions. It is separate from Engine so engines that
// can't provide positions (purego) need no change; check for it with a type
// assertion.
//
// Layout returns the same errors as Engine.Pages: an error matching
// extract.ErrMalformedSource for input the engine can't parse (or output it
// can't trust), and a context error when ctx is canceled.
type LayoutEngine interface {
	Layout(ctx context.Context, data []byte) (*Layout, error)
}

// Layout is every page of a PDF with its size and words. It has JSON tags
// because consumers persist it beside their text.
type Layout struct {
	// Version is the serialized form's version, LayoutVersion when produced.
	Version int `json:"version"`
	// Engine is the producing engine's Name().
	Engine string `json:"engine"`
	// Box names the page box coordinates are relative to (BoxCrop).
	Box string `json:"box"`
	// Pages has every page in order, including blank and scanned pages, which
	// have a size but no words.
	Pages []PageLayout `json:"pages"`
}

// PageLayout is one page's size and words.
type PageLayout struct {
	// Number is the 1-based page number.
	Number int `json:"number"`
	// Width and Height are the page box size in points as displayed, i.e.
	// after rotation: a portrait page with /Rotate 90 is wider than tall. They
	// match what a renderer produces for the same box.
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	// Rotation is the page's declared rotation, 0, 90, 180 or 270. It is
	// informational: Width, Height and word boxes are already rotated.
	Rotation int `json:"rotation"`
	// Words are in the engine's reading order. Never nil; empty for a page
	// without a text layer.
	Words []Word `json:"words"`
}

// Word is one word with its box and the engine's structure ids.
type Word struct {
	// Text is the word as the engine reported it.
	Text string `json:"text"`
	// Box is in points, origin at the top-left of the page box as displayed,
	// Y growing down. Coordinates are finite but can lie far off the page (a
	// valid PDF can place text anywhere), so never size anything from them.
	Box Rect `json:"box"`
	// Block and Line identify the word's block and line; each is unique within
	// the page (not across pages), numbered from 0 in reading order.
	Block int `json:"block"`
	Line  int `json:"line"`
	// Conf is the engine's confidence for the word, 0–100, or -1 when it
	// reports none. pdftotext reports 100 for every text-layer word.
	Conf int `json:"conf"`
}

// Rect is an axis-aligned box: (X0, Y0) top-left, (X1, Y1) bottom-right.
type Rect struct {
	X0 float64 `json:"x0"`
	Y0 float64 `json:"y0"`
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
}
