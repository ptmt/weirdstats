package web

import (
	"net/http/httptest"
	"testing"

	"weirdstats/internal/gps"
)

func TestBuildRouteSpeedStyleSmoothsSpikeAndKeepsFastSectionHot(t *testing.T) {
	points := make([]gps.Point, 20)
	for i := range points {
		points[i].Speed = 3
		if i >= 10 {
			points[i].Speed = 10
		}
	}
	points[3].Speed = 45 // A single GPS spike must not set the color range.
	style := buildRouteSpeedStyle(points, 0.5)
	if !style.Available || len(style.Colors) != len(points)-1 {
		t.Fatalf("unexpected speed style: %+v", style)
	}
	if style.SlowKmh != 11 || style.FastKmh != 36 {
		t.Fatalf("unexpected legend range: %d..%d km/h", style.SlowKmh, style.FastKmh)
	}
	if style.Colors[2] != routeSpeedPalette[0] || style.Colors[17] != routeSpeedPalette[len(routeSpeedPalette)-1] {
		t.Fatalf("slow and fast sections have wrong colors: %q, %q", style.Colors[2], style.Colors[17])
	}
}

func TestBuildRouteSpeedStyleDisablesMissingSpeed(t *testing.T) {
	style := buildRouteSpeedStyle(make([]gps.Point, 3), 0.5)
	if style.Available || len(style.Colors) != 2 || style.Colors[0] != routeStoppedColor {
		t.Fatalf("unexpected missing-speed style: %+v", style)
	}
}

func TestPosterLensAndExportFormatOptions(t *testing.T) {
	for _, test := range []struct {
		query  string
		lens   string
		format string
		width  int
		height int
	}{
		{"", "clean", "story", 1080, 1920},
		{"?lens=speed&format=portrait", "speed", "portrait", 1080, 1350},
		{"?lens=speed&format=square", "speed", "square", 1080, 1080},
		{"?lens=bogus&format=bogus", "clean", "story", 1080, 1920},
	} {
		request := httptest.NewRequest("GET", "/activity/1/poster"+test.query, nil)
		options := posterRenderOptionsFromRequest(request)
		width, height := posterExportDimensions(options.Format)
		if options.Lens != test.lens || options.Format != test.format || width != test.width || height != test.height {
			t.Fatalf("%q: got lens %q, format %q, size %dx%d", test.query, options.Lens, options.Format, width, height)
		}
	}
}
