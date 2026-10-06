package peakfs

import (
	"reflect"
	"testing"

	"github.com/aleksana/peak/internal/vfs/afero"
)

func TestWatch(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/1", 0755)
	fs.MkdirAll("/2", 0755)
	fs.MkdirAll("/srv", 0755)            // not a window
	afero.WriteFile(fs, "/3", nil, 0644) // numeric, but not a directory
	afero.WriteFile(fs, "/event", []byte(
		"new 4 /tmp/a file.go\n"+
			"get 4 /tmp/a file.go\n"+
			"close 1 \n"+
			"garbage\n"), 0644)

	var got []Event
	if err := Watch(fs, func(e Event) { got = append(got, e) }); err != nil {
		t.Fatal(err)
	}

	want := []Event{
		{Kind: "new", ID: 1},
		{Kind: "new", ID: 2},
		{Kind: "new", ID: 4, Name: "/tmp/a file.go"},
		{Kind: "get", ID: 4, Name: "/tmp/a file.go"},
		{Kind: "close", ID: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events =\n%v\nwant\n%v", got, want)
	}
}
